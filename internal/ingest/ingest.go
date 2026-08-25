// Package ingest coordinates deterministic discovery, parsing, and SQLite storage.
package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FormlessEvoker/clogs/internal/model"
	"github.com/FormlessEvoker/clogs/internal/parser/access"
	"github.com/FormlessEvoker/clogs/internal/parser/catalina"
	"github.com/FormlessEvoker/clogs/internal/parser/jvmmultiline"
)

type Options struct {
	Input, Source, Timezone, Database string
	StoreRaw, Strict                  bool
	RouteTemplates                    []string
}
type Summary struct {
	FilesSeen, FilesIngested, FilesSkipped, FilesFailed, Events, Malformed, Warnings int
	Families                                                                         map[model.Family]int
	Files                                                                            []FileResult
}

type FileResult struct {
	Path, Status, Reason string
	Events               int
	Family               model.Family
}

func Run(ctx context.Context, db *sql.DB, options Options) (Summary, error) {
	var summary Summary
	supported := 0
	summary.Families = make(map[model.Family]int)
	files, root, err := discover(options.Input, options.Database)
	if err != nil {
		return summary, err
	}
	runID, err := beginRun(db, options)
	if err != nil {
		return summary, err
	}
	finished := false
	defer func() {
		if !finished {
			_ = finishRun(db, runID, summary, "failed", "ingestion interrupted")
		}
	}()
	for _, path := range files {
		summary.FilesSeen++
		relative, _ := filepath.Rel(root, path)
		status, events, malformed, warnings, family, fileErr := ingestFile(ctx, db, path, relative, runID, options)
		if family != "" {
			supported++
		}
		summary.Malformed += malformed
		summary.Warnings += warnings
		switch status {
		case "ingested":
			summary.FilesIngested++
			summary.Events += events
			summary.Families[family] += events
		case "skipped":
			summary.FilesSkipped++
		case "failed":
			summary.FilesFailed++
		}
		fileResult := FileResult{Path: relative, Status: status, Events: events, Family: family}
		if status == "skipped" {
			if family == "" {
				fileResult.Reason = "unrecognized or empty content"
			} else {
				fileResult.Reason = "exact duplicate source/path/content"
			}
		}
		if fileErr != nil {
			fileResult.Reason = fileErr.Error()
			summary.Files = append(summary.Files, fileResult)
			continue
		}
		summary.Files = append(summary.Files, fileResult)
	}
	if supported == 0 {
		return summary, fmt.Errorf("no supported log files found")
	}
	status, message := "completed", ""
	if summary.FilesFailed > 0 {
		status, message = "failed", fmt.Sprintf("%d file(s) failed", summary.FilesFailed)
	}
	if err := finishRun(db, runID, summary, status, message); err != nil {
		return summary, err
	}
	finished = true
	if summary.FilesFailed > 0 {
		return summary, fmt.Errorf("%s", message)
	}
	return summary, nil
}

func discover(input, database string) ([]string, string, error) {
	info, err := os.Stat(input)
	if err != nil {
		return nil, "", fmt.Errorf("stat input %q: %w", input, err)
	}
	inputAbs, _ := filepath.Abs(input)
	dbAbs, _ := filepath.Abs(database)
	if !info.IsDir() {
		if inputAbs == dbAbs {
			return nil, "", fmt.Errorf("database cannot be an ingestion input")
		}
		return []string{input}, filepath.Dir(input), nil
	}
	var files []string
	err = filepath.WalkDir(input, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() && path != database {
			abs, _ := filepath.Abs(path)
			if abs != dbAbs && abs != dbAbs+"-wal" && abs != dbAbs+"-shm" {
				files = append(files, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("scan input: %w", err)
	}
	sort.Strings(files)
	return files, input, nil
}

func ingestFile(ctx context.Context, db *sql.DB, path, relative string, runID int64, options Options) (string, int, int, int, model.Family, error) {
	hash, size, modified, prefix, err := fingerprint(path)
	if err != nil {
		return "failed", 0, 0, 0, "", err
	}
	family, parserVersion := model.Family(""), ""
	if jvmmultiline.Detect(prefix) > 0 {
		family, parserVersion = model.FamilyJVMMultiline, "jvm-multiline-v1"
	}
	if family == "" && access.Detect(prefix) > 0 {
		family, parserVersion = model.FamilyAccess, "access-v1"
	}
	if family == "" && catalina.Detect(prefix) > 0 {
		family, parserVersion = model.FamilyCatalina, "catalina-v1"
	}
	if family == "" {
		return "skipped", 0, 0, 0, "", nil
	}
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM source_files WHERE source_label=? AND relative_path=? AND sha256=?`, options.Source, relative, hash).Scan(&exists); err != nil {
		return "failed", 0, 0, 0, "", err
	}
	if exists > 0 {
		return "skipped", 0, 0, 0, family, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "failed", 0, 0, 0, "", err
	}
	defer tx.Rollback()
	fileTimezone := any(nil)
	if family == model.FamilyJVMMultiline || family == model.FamilyCatalina {
		fileTimezone = options.Timezone
	}
	result, err := tx.Exec(`INSERT INTO source_files(ingest_run_id,source_label,path,relative_path,sha256,size_bytes,modified_at_ns,detected_family,timezone,parser_version,ingested_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, runID, options.Source, path, relative, hash, size, modified, family, fileTimezone, parserVersion, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "failed", 0, 0, 0, "", err
	}
	sourceID, _ := result.LastInsertId()
	statements, err := prepareStatements(tx)
	if err != nil {
		return "failed", 0, 0, 0, "", err
	}
	defer statements.close()
	file, err := os.Open(path)
	if err != nil {
		return "failed", 0, 0, 0, "", err
	}
	defer file.Close()
	events, warnings, ordinal := 0, 0, 0
	emit := func(event model.Event) error {
		ordinal++
		event.SourceLabel = options.Source
		warnings += len(event.ParseWarnings)
		events++
		return statements.insert(sourceID, ordinal, event, options.StoreRaw)
	}
	malformed := 0
	if family == model.FamilyJVMMultiline || family == model.FamilyCatalina {
		if options.Timezone == "" {
			return "failed", 0, 0, 0, family, fmt.Errorf("%s input %q requires --timezone", family, path)
		}
		location, err := time.LoadLocation(options.Timezone)
		if err != nil {
			return "failed", 0, 0, 0, family, fmt.Errorf("invalid timezone %q: %w", options.Timezone, err)
		}
		if family == model.FamilyJVMMultiline {
			parsed, parseErr := jvmmultiline.Parse(ctx, file, jvmmultiline.Options{Timezone: location, SourcePath: relative}, emit)
			err = parseErr
			malformed = int(parsed.OrphanLines)
		} else {
			parsed, parseErr := catalina.Parse(ctx, file, catalina.Options{Timezone: location, SourcePath: relative}, emit)
			err = parseErr
			malformed = int(parsed.OrphanLines)
		}
	} else {
		parsed, parseErr := access.Parse(ctx, file, access.Options{SourcePath: relative, RouteTemplates: options.RouteTemplates}, emit)
		err = parseErr
		malformed = len(parsed.Malformed)
	}
	if err != nil {
		return "failed", 0, malformed, warnings, family, fmt.Errorf("parse %q: %w", path, err)
	}
	if options.Strict && (malformed > 0 || warnings > 0) {
		return "failed", 0, malformed, warnings, family, fmt.Errorf("strict mode rejected %q: %d malformed line(s), %d warning(s)", path, malformed, warnings)
	}
	if _, err := tx.Exec(`UPDATE source_files SET event_count=?, malformed_line_count=? WHERE id=?`, events, malformed, sourceID); err != nil {
		return "failed", 0, malformed, warnings, family, err
	}
	if err := tx.Commit(); err != nil {
		return "failed", 0, malformed, warnings, family, err
	}
	return "ingested", events, malformed, warnings, family, nil
}

func fingerprint(path string) (string, int64, int64, []byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, 0, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, 0, nil, err
	}
	prefix := make([]byte, 32<<10)
	n, readErr := file.Read(prefix)
	if readErr != nil && readErr != io.EOF {
		return "", 0, 0, nil, readErr
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", 0, 0, nil, err
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", 0, 0, nil, err
	}
	return hex.EncodeToString(sum.Sum(nil)), info.Size(), info.ModTime().UnixNano(), prefix[:n], nil
}

type statements struct{ signatureInsert, signatureID, event, java, http *sql.Stmt }

func prepareStatements(tx *sql.Tx) (*statements, error) {
	insert, err := tx.Prepare(`INSERT OR IGNORE INTO signatures(fingerprint,algorithm_version,family,severity,namespace,operation,message_template,exception_class) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	lookup, err := tx.Prepare(`SELECT id FROM signatures WHERE fingerprint=?`)
	if err != nil {
		insert.Close()
		return nil, err
	}
	event, err := tx.Prepare(`INSERT INTO events(source_file_id,signature_id,family,occurred_at_ns,occurred_at_utc,original_timestamp,timestamp_precision,source_line_start,source_line_end,source_ordinal,severity,message,raw_text,parse_warnings) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		insert.Close()
		lookup.Close()
		return nil, err
	}
	java, err := tx.Prepare(`INSERT INTO java_details(event_id,logger,operation,thread,protocol_type,exception_class,exception_message,root_cause_class,root_cause_message,stack_trace) VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		insert.Close()
		lookup.Close()
		event.Close()
		return nil, err
	}
	http, err := tx.Prepare(`INSERT INTO http_details(event_id,client_address,server_port,method,raw_target,path,raw_query,route_template,site,http_version,status,response_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		insert.Close()
		lookup.Close()
		event.Close()
		java.Close()
		return nil, err
	}
	return &statements{insert, lookup, event, java, http}, nil
}
func (s *statements) close() {
	s.signatureInsert.Close()
	s.signatureID.Close()
	s.event.Close()
	s.java.Close()
	s.http.Close()
}

func (s *statements) insert(sourceID int64, ordinal int, event model.Event, raw bool) error {
	java := event.Java
	logger, operation, exception := "", "", ""
	if java != nil {
		logger, operation, exception = java.Logger, java.Operation, java.ExceptionClass
	}
	if _, err := s.signatureInsert.Exec(event.Signature, 1, event.Family, event.Severity, logger, operation, event.Message, exception); err != nil {
		return err
	}
	var signatureID int64
	if err := s.signatureID.QueryRow(event.Signature).Scan(&signatureID); err != nil {
		return err
	}
	var warnings any
	if len(event.ParseWarnings) > 0 {
		encoded, _ := json.Marshal(event.ParseWarnings)
		warnings = string(encoded)
	}
	var rawText any
	if raw {
		rawText = event.RawText
	}
	result, err := s.event.Exec(sourceID, signatureID, event.Family, event.OccurredAt.UnixNano(), event.OccurredAt.UTC().Format(time.RFC3339Nano), event.OriginalTimestamp, event.Precision, event.SourceLineStart, event.SourceLineEnd, ordinal, event.Severity, event.Message, rawText, warnings)
	if err != nil {
		return err
	}
	eventID, _ := result.LastInsertId()
	if java == nil {
		if event.HTTP == nil {
			return nil
		}
		http := event.HTTP
		var bytes any
		if http.ResponseBytes != nil {
			bytes = *http.ResponseBytes
		}
		_, err = s.http.Exec(eventID, http.ClientAddress, http.ServerPort, http.Method, http.RawTarget, http.Path, http.RawQuery, http.RouteTemplate, http.Site, http.HTTPVersion, http.Status, bytes)
		return err
	}
	_, err = s.java.Exec(eventID, java.Logger, java.Operation, java.Thread, java.ProtocolType, java.ExceptionClass, java.ExceptionMessage, java.RootCauseClass, java.RootCauseMessage, java.StackTrace)
	return err
}

func beginRun(db *sql.DB, o Options) (int64, error) {
	result, err := db.Exec(`INSERT INTO ingest_runs(started_at,input_path,source_label,timezone,status) VALUES(?,?,?,?,?)`, time.Now().UTC().Format(time.RFC3339Nano), o.Input, o.Source, o.Timezone, "running")
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
func finishRun(db *sql.DB, id int64, s Summary, status, message string) error {
	_, err := db.Exec(`UPDATE ingest_runs SET completed_at=?,files_seen=?,files_ingested=?,files_skipped=?,events_ingested=?,malformed_lines=?,status=?,error_message=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), s.FilesSeen, s.FilesIngested, s.FilesSkipped, s.Events, s.Malformed, status, nullString(message), id)
	return err
}
func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func SummaryText(summary Summary, source, database string) string {
	var text strings.Builder
	for _, file := range summary.Files {
		fmt.Fprintf(&text, "%s: %s", file.Status, file.Path)
		if file.Family != "" {
			fmt.Fprintf(&text, " (%s, %d events)", file.Family, file.Events)
		}
		if file.Reason != "" {
			fmt.Fprintf(&text, ": %s", file.Reason)
		}
		text.WriteByte('\n')
	}
	fmt.Fprintf(&text, "Source:           %s\nFiles seen:       %d\nFiles ingested:   %d\nFiles skipped:    %d\nFiles failed:     %d\nJVM events:       %d\nAccess events:    %d\nCatalina events:  %d\nMalformed lines:  %d\nWarnings:         %d\nDatabase: %s\n", source, summary.FilesSeen, summary.FilesIngested, summary.FilesSkipped, summary.FilesFailed, summary.Families[model.FamilyJVMMultiline], summary.Families[model.FamilyAccess], summary.Families[model.FamilyCatalina], summary.Malformed, summary.Warnings, strings.TrimSpace(database))
	return text.String()
}
