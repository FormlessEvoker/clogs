// Package report reads canonical SQLite events for export and summary commands.
package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Event struct {
	SourceLabel       string   `json:"source_label"`
	SourcePath        string   `json:"source_path"`
	Family            string   `json:"family"`
	OccurredAtNS      int64    `json:"-"`
	OccurredAt        string   `json:"occurred_at"`
	OriginalTimestamp string   `json:"original_timestamp"`
	Precision         string   `json:"precision"`
	SourceLineStart   int64    `json:"source_line_start"`
	SourceLineEnd     int64    `json:"source_line_end"`
	Severity          string   `json:"severity,omitempty"`
	Message           string   `json:"message"`
	Signature         string   `json:"signature"`
	RawText           *string  `json:"raw_text,omitempty"`
	ParseWarnings     []string `json:"parse_warnings,omitempty"`
	Logger            string   `json:"logger,omitempty"`
	Operation         string   `json:"operation,omitempty"`
	Thread            string   `json:"thread,omitempty"`
	ProtocolType      string   `json:"protocol_type,omitempty"`
	ExceptionClass    string   `json:"exception_class,omitempty"`
	ExceptionMessage  string   `json:"exception_message,omitempty"`
	RootCauseClass    string   `json:"root_cause_class,omitempty"`
	RootCauseMessage  string   `json:"root_cause_message,omitempty"`
	StackTrace        string   `json:"stack_trace,omitempty"`
	ClientAddress     string   `json:"client_address,omitempty"`
	ServerPort        *int64   `json:"server_port,omitempty"`
	Method            string   `json:"method,omitempty"`
	RawTarget         string   `json:"raw_target,omitempty"`
	Path              string   `json:"path,omitempty"`
	RawQuery          string   `json:"raw_query,omitempty"`
	RouteTemplate     string   `json:"route_template,omitempty"`
	Site              string   `json:"site,omitempty"`
	HTTPVersion       string   `json:"http_version,omitempty"`
	Status            *int64   `json:"status,omitempty"`
	ResponseBytes     *int64   `json:"response_bytes,omitempty"`
}

func Events(ctx context.Context, db *sql.DB, source string, includeRaw bool) ([]Event, error) {
	return eventsBetween(ctx, db, source, includeRaw, nil, nil)
}

func eventsBetween(ctx context.Context, db *sql.DB, source string, includeRaw bool, start, end *int64) ([]Event, error) {
	rows, err := db.QueryContext(ctx, `SELECT sf.source_label,sf.relative_path,e.family,e.occurred_at_ns,e.occurred_at_utc,e.original_timestamp,e.timestamp_precision,e.source_line_start,e.source_line_end,COALESCE(e.severity,''),e.message,s.fingerprint,e.raw_text,e.parse_warnings,COALESCE(j.logger,''),COALESCE(j.operation,''),COALESCE(j.thread,''),COALESCE(j.protocol_type,''),COALESCE(j.exception_class,''),COALESCE(j.exception_message,''),COALESCE(j.root_cause_class,''),COALESCE(j.root_cause_message,''),COALESCE(j.stack_trace,''),COALESCE(h.client_address,''),h.server_port,COALESCE(h.method,''),COALESCE(h.raw_target,''),COALESCE(h.path,''),COALESCE(h.raw_query,''),COALESCE(h.route_template,''),COALESCE(h.site,''),COALESCE(h.http_version,''),h.status,h.response_bytes FROM events e JOIN source_files sf ON sf.id=e.source_file_id JOIN signatures s ON s.id=e.signature_id LEFT JOIN java_details j ON j.event_id=e.id LEFT JOIN http_details h ON h.event_id=e.id WHERE (?='' OR sf.source_label=?) AND (? IS NULL OR e.occurred_at_ns>=?) AND (? IS NULL OR e.occurred_at_ns<=?) ORDER BY e.occurred_at_ns,sf.source_label,sf.relative_path,e.source_line_start,e.id`, source, source, start, start, end, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var event Event
		var raw, warnings sql.NullString
		if err := rows.Scan(&event.SourceLabel, &event.SourcePath, &event.Family, &event.OccurredAtNS, &event.OccurredAt, &event.OriginalTimestamp, &event.Precision, &event.SourceLineStart, &event.SourceLineEnd, &event.Severity, &event.Message, &event.Signature, &raw, &warnings, &event.Logger, &event.Operation, &event.Thread, &event.ProtocolType, &event.ExceptionClass, &event.ExceptionMessage, &event.RootCauseClass, &event.RootCauseMessage, &event.StackTrace, &event.ClientAddress, &event.ServerPort, &event.Method, &event.RawTarget, &event.Path, &event.RawQuery, &event.RouteTemplate, &event.Site, &event.HTTPVersion, &event.Status, &event.ResponseBytes); err != nil {
			return nil, err
		}
		if includeRaw && raw.Valid {
			value := raw.String
			event.RawText = &value
		}
		if warnings.Valid {
			if err := json.Unmarshal([]byte(warnings.String), &event.ParseWarnings); err != nil {
				return nil, fmt.Errorf("decode parse warnings: %w", err)
			}
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func WriteNDJSON(ctx context.Context, db *sql.DB, source string, includeRaw bool, output io.Writer) error {
	events, err := Events(ctx, db, source, includeRaw)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func WriteCSV(ctx context.Context, db *sql.DB, source string, includeRaw bool, output io.Writer) error {
	events, err := Events(ctx, db, source, includeRaw)
	if err != nil {
		return err
	}
	writer := csvWriter{output: output}
	if err := writer.row(headers); err != nil {
		return err
	}
	for _, e := range events {
		if err := writer.row([]string{e.SourceLabel, e.SourcePath, e.Family, e.OccurredAt, e.OriginalTimestamp, e.Precision, strconv.FormatInt(e.SourceLineStart, 10), strconv.FormatInt(e.SourceLineEnd, 10), e.Severity, e.Message, e.Signature, optional(e.RawText), strings.Join(e.ParseWarnings, " | "), e.Logger, e.Operation, e.Thread, e.ProtocolType, e.ExceptionClass, e.ExceptionMessage, e.RootCauseClass, e.RootCauseMessage, e.StackTrace, e.ClientAddress, optionalInt(e.ServerPort), e.Method, e.RawTarget, e.Path, e.RawQuery, e.RouteTemplate, e.Site, e.HTTPVersion, optionalInt(e.Status), optionalInt(e.ResponseBytes)}); err != nil {
			return err
		}
	}
	return writer.flush()
}

var headers = []string{"source_label", "source_path", "family", "occurred_at", "original_timestamp", "precision", "source_line_start", "source_line_end", "severity", "message", "signature", "raw_text", "parse_warnings", "logger", "operation", "thread", "protocol_type", "exception_class", "exception_message", "root_cause_class", "root_cause_message", "stack_trace", "client_address", "server_port", "method", "raw_target", "path", "raw_query", "route_template", "site", "http_version", "status", "response_bytes"}

func optional(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func optionalInt(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}
