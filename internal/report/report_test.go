package report

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FormlessEvoker/clogs/internal/ingest"
	"github.com/FormlessEvoker/clogs/internal/storage"
)

func TestExportsAreDeterministicAndCarryProvenance(t *testing.T) {
	db := testDatabase(t)
	var first, second bytes.Buffer
	if err := WriteNDJSON(context.Background(), db, "one", false, &first); err != nil {
		t.Fatal(err)
	}
	if err := WriteNDJSON(context.Background(), db, "one", false, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("NDJSON export is not deterministic")
	}
	for _, line := range strings.Split(strings.TrimSpace(first.String()), "\n") {
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatal(err)
		}
		if value["source_label"] != "one" || value["source_path"] == nil {
			t.Fatalf("missing provenance: %#v", value)
		}
		if _, exists := value["raw_text"]; exists {
			t.Fatalf("raw text unexpectedly exported: %#v", value)
		}
	}
	var withRaw bytes.Buffer
	if err := WriteNDJSON(context.Background(), db, "one", true, &withRaw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withRaw.String(), "raw_text") {
		t.Fatal("raw text missing when requested")
	}
	var csvOutput bytes.Buffer
	var csvWithoutRaw bytes.Buffer
	if err := WriteCSV(context.Background(), db, "one", false, &csvWithoutRaw); err != nil {
		t.Fatal(err)
	}
	if err := WriteCSV(context.Background(), db, "one", true, &csvOutput); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(csvOutput.String())).ReadAll()
	if err != nil || len(records) != 8 || strings.Join(records[0], ",") != strings.Join(headers, ",") {
		t.Fatalf("records=%d err=%v output=%q", len(records), err, csvOutput.String())
	}
	if records[1][9] == "" || records[1][11] == "" {
		t.Fatalf("CSV did not preserve quoted message/raw fields: %#v", records[1])
	}
	withoutRaw, err := csv.NewReader(strings.NewReader(csvWithoutRaw.String())).ReadAll()
	if err != nil || withoutRaw[1][11] != "" {
		t.Fatalf("raw omitted records=%#v err=%v", withoutRaw, err)
	}
	all, err := Events(context.Background(), db, "", false)
	if err != nil || len(all) != 14 || all[0].SourceLabel != "one" || all[len(all)-1].SourceLabel != "two" {
		t.Fatalf("events=%#v err=%v", all, err)
	}
}

func TestEventsOrderTiesAndFilterBySource(t *testing.T) {
	db := orderedExportDatabase(t)
	events, err := Events(context.Background(), db, "", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha-z", exportSpecialMessage, "beta-a-second", "beta-a-next-line", "beta-z"}
	if len(events) != len(want) {
		t.Fatalf("events=%#v", events)
	}
	for index, message := range want {
		if events[index].Message != message {
			t.Fatalf("events[%d].Message=%q want %q", index, events[index].Message, message)
		}
		if events[index].RawText != nil {
			t.Fatalf("events[%d] unexpectedly includes raw text", index)
		}
	}
	filtered, err := Events(context.Background(), db, "beta", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 4 || filtered[0].SourceLabel != "beta" || filtered[0].SourcePath != "a.log" || filtered[3].SourcePath != "z.log" {
		t.Fatalf("filtered=%#v", filtered)
	}
	if filtered[0].RawText == nil || *filtered[0].RawText == "" {
		t.Fatalf("raw text not retained in filtered event: %#v", filtered[0])
	}
}

func TestNDJSONFieldPresenceAndRawControl(t *testing.T) {
	db := orderedExportDatabase(t)
	var withoutRaw, withRaw bytes.Buffer
	if err := WriteNDJSON(context.Background(), db, "", false, &withoutRaw); err != nil {
		t.Fatal(err)
	}
	if err := WriteNDJSON(context.Background(), db, "", true, &withRaw); err != nil {
		t.Fatal(err)
	}
	without := decodeNDJSON(t, withoutRaw.String())
	with := decodeNDJSON(t, withRaw.String())
	for _, record := range without {
		if _, found := record["raw_text"]; found {
			t.Fatalf("raw text emitted without request: %#v", record)
		}
	}
	assertExactJSONKeys(t, with["alpha-z"], []string{"family", "message", "occurred_at", "original_timestamp", "precision", "signature", "source_label", "source_line_end", "source_line_start", "source_path"})

	first := with[exportSpecialMessage]
	if first["source_label"] != "beta" || first["source_path"] != "a.log" || first["raw_text"] != "raw, \"quoted\"\nline" {
		t.Fatalf("provenance/raw=%#v", first)
	}
	if warnings, ok := first["parse_warnings"].([]any); !ok || len(warnings) != 2 || warnings[0] != "first warning" || warnings[1] != "second warning" {
		t.Fatalf("parse_warnings=%#v", first["parse_warnings"])
	}
	if first["logger"] != "example.Logger" || first["status"] != float64(503) || first["server_port"] != float64(8443) {
		t.Fatalf("structured details=%#v", first)
	}
	if _, found := with["alpha-z"]["parse_warnings"]; found {
		t.Fatalf("empty warnings should be omitted: %#v", with["alpha-z"])
	}
	if _, found := with["alpha-z"]["raw_text"]; found {
		t.Fatalf("missing stored raw text should be omitted: %#v", with["alpha-z"])
	}
	for _, field := range []string{"severity", "logger", "operation", "thread", "protocol_type", "exception_class", "exception_message", "root_cause_class", "root_cause_message", "stack_trace", "client_address", "server_port", "method", "raw_target", "path", "raw_query", "route_template", "site", "http_version", "status", "response_bytes"} {
		if _, found := with["alpha-z"][field]; found {
			t.Fatalf("empty or nil optional field %q should be omitted: %#v", field, with["alpha-z"])
		}
	}
}

func TestCSVExportUsesFixedColumnsAndEscapesFields(t *testing.T) {
	db := orderedExportDatabase(t)
	var withRaw, withoutRaw bytes.Buffer
	if err := WriteCSV(context.Background(), db, "", true, &withRaw); err != nil {
		t.Fatal(err)
	}
	if err := WriteCSV(context.Background(), db, "", false, &withoutRaw); err != nil {
		t.Fatal(err)
	}
	wantHeader := []string{"source_label", "source_path", "family", "occurred_at", "original_timestamp", "precision", "source_line_start", "source_line_end", "severity", "message", "signature", "raw_text", "parse_warnings", "logger", "operation", "thread", "protocol_type", "exception_class", "exception_message", "root_cause_class", "root_cause_message", "stack_trace", "client_address", "server_port", "method", "raw_target", "path", "raw_query", "route_template", "site", "http_version", "status", "response_bytes"}
	withRecords, err := csv.NewReader(strings.NewReader(withRaw.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(withRecords) != 6 || strings.Join(withRecords[0], "|") != strings.Join(wantHeader, "|") {
		t.Fatalf("header/records=%#v", withRecords)
	}
	if !strings.Contains(withRaw.String(), `"message, ""quoted""
next"`) || !strings.Contains(withRaw.String(), `"raw, ""quoted""
line"`) {
		t.Fatalf("CSV did not quote embedded delimiters: %q", withRaw.String())
	}
	first := withRecords[2]
	if first[0] != "beta" || first[1] != "a.log" || first[9] != "message, \"quoted\"\nnext" || first[11] != "raw, \"quoted\"\nline" || first[12] != "first warning | second warning" || first[13] != "example.Logger" || first[23] != "8443" || first[31] != "503" {
		t.Fatalf("first ordered record=%#v", first)
	}
	withoutRecords, err := csv.NewReader(strings.NewReader(withoutRaw.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if withoutRecords[2][11] != "" {
		t.Fatalf("raw column without request=%#v", withoutRecords[2])
	}
}

func TestExportPropagatesOutputWriterErrors(t *testing.T) {
	db := orderedExportDatabase(t)
	for name, write := range map[string]func() error{
		"ndjson": func() error { return WriteNDJSON(context.Background(), db, "", false, failingWriter{}) },
		"csv":    func() error { return WriteCSV(context.Background(), db, "", false, failingWriter{}) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(); !errors.Is(err, errTestWriter) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

var errTestWriter = errors.New("intentional output failure")

const exportSpecialMessage = "message, \"quoted\"\nnext"

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errTestWriter }

func decodeNDJSON(t *testing.T, output string) map[string]map[string]any {
	t.Helper()
	values := make(map[string]map[string]any)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatal(err)
		}
		values[value["message"].(string)] = value
	}
	return values
}

func assertExactJSONKeys(t *testing.T, value map[string]any, want []string) {
	t.Helper()
	if len(value) != len(want) {
		t.Fatalf("keys=%v want=%v", jsonKeys(value), want)
	}
	for _, key := range want {
		if _, found := value[key]; !found {
			t.Fatalf("keys=%v missing %q", jsonKeys(value), key)
		}
	}
}

func jsonKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	return keys
}

func orderedExportDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "clogs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	result, err := db.Exec(`INSERT INTO ingest_runs(started_at,input_path,source_label,status) VALUES(?,?,?,?)`, "2026-01-01T00:00:00Z", "seed", "seed", "complete")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	addSource := func(label, path string) int64 {
		t.Helper()
		result, err := db.Exec(`INSERT INTO source_files(ingest_run_id,source_label,path,relative_path,sha256,size_bytes,detected_family,parser_version,ingested_at) VALUES(?,?,?,?,?,?,?,?,?)`, runID, label, path, path, label+"-"+path, 1, "access", "seed", "2026-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sources := map[string]int64{
		"alpha/z.log": addSource("alpha", "z.log"),
		"beta/a.log":  addSource("beta", "a.log"),
		"beta/z.log":  addSource("beta", "z.log"),
	}
	result, err = db.Exec(`INSERT INTO signatures(fingerprint,algorithm_version,family) VALUES(?,?,?)`, "seed-signature", 1, "access")
	if err != nil {
		t.Fatal(err)
	}
	signatureID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	addEvent := func(sourceID, line int64, severity, message string, raw, warnings any) int64 {
		t.Helper()
		result, err := db.Exec(`INSERT INTO events(source_file_id,signature_id,family,occurred_at_ns,occurred_at_utc,original_timestamp,timestamp_precision,source_line_start,source_line_end,source_ordinal,severity,message,raw_text,parse_warnings) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, sourceID, signatureID, "access", 100, "2026-01-01T00:00:00Z", "seed", "second", line, line, line, severity, message, raw, warnings)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	addEvent(sources["alpha/z.log"], 2, "", "alpha-z", nil, nil)
	firstID := addEvent(sources["beta/a.log"], 1, "WARN", exportSpecialMessage, "raw, \"quoted\"\nline", `["first warning","second warning"]`)
	addEvent(sources["beta/a.log"], 1, "WARN", "beta-a-second", nil, nil)
	addEvent(sources["beta/a.log"], 2, "WARN", "beta-a-next-line", nil, nil)
	addEvent(sources["beta/z.log"], 1, "WARN", "beta-z", nil, nil)
	if _, err := db.Exec(`INSERT INTO java_details(event_id,logger,operation,thread,protocol_type) VALUES(?,?,?,?,?)`, firstID, "example.Logger", "process", "worker", "seed"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO http_details(event_id,client_address,server_port,method,raw_target,path,raw_query,route_template,site,http_version,status,response_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, firstID, "192.0.2.1", 8443, "GET", "/items?x=1", "/items", "x=1", "/items", "site-a", "HTTP/1.1", 503, 5); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCSVWriterQuotesStructuredFields(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	writer := csvWriter{output: &output}
	values := []string{"comma,value", `quote"value`, "line one\nline two"}
	if err := writer.row(values); err != nil {
		t.Fatal(err)
	}
	if err := writer.flush(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"comma,value"`) || !strings.Contains(output.String(), `"quote""value"`) {
		t.Fatalf("CSV did not quote fields: %q", output.String())
	}
	records, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
	if err != nil || len(records) != 1 || strings.Join(records[0], "|") != strings.Join(values, "|") {
		t.Fatalf("records=%#v err=%v", records, err)
	}
}

func TestStatsSupportsSourceFilter(t *testing.T) {
	db := testDatabase(t)
	stats, err := CollectStats(context.Background(), db, "one")
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourceFiles != 3 || count(stats.Families, "jvm-multiline") != 1 || count(stats.Families, "access") != 5 || count(stats.Families, "catalina") != 1 || count(stats.HTTPStatusClasses, "5xx") != 1 {
		t.Fatalf("stats=%#v", stats)
	}
	all, err := CollectStats(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if all.SourceFiles != 6 || count(all.Sources, "one") != 7 || count(all.Sources, "two") != 7 {
		t.Fatalf("all=%#v", all)
	}
}

func TestAnalyzeMergedTimelineAndIncidentRelationships(t *testing.T) {
	db := testDatabase(t)
	around, _ := time.Parse(time.RFC3339, "2026-08-05T12:03:31Z")
	options := QueryOptions{Around: around, Before: 2 * time.Second, After: 2 * time.Second, Bucket: time.Second, QuietPeriod: 30 * time.Second, PreWindow: 2 * time.Second, CorrelationWindow: 2 * time.Second, Source: "one", Status: "500"}
	report, err := Analyze(context.Background(), db, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.BaselineSize != 6 || report.SampleSize != 1 || len(report.Timeline) != 1 || report.Timeline[0].Family != "access" {
		t.Fatalf("report=%#v", report)
	}
	if len(report.Routes) != 1 || report.Routes[0].Requests != 4 || report.Routes[0].Failures != 1 || report.Routes[0].FailureRate != .25 {
		t.Fatalf("routes=%#v", report.Routes)
	}
	if len(report.RequestSignals) == 0 || report.RequestSignals[0].Route != "/route" || report.RequestSignals[0].SpikeCount == 0 || report.RequestSignals[0].PreRequests != 3 || report.RequestSignals[0].PriorRequests != 1 || report.RequestSignals[0].PreResponseBytes == 0 {
		t.Fatalf("request_signals=%#v", report.RequestSignals)
	}
	if len(report.RequestBursts) == 0 || !strings.Contains(report.RequestBursts[0].Fingerprint, "GET") || report.RequestBursts[0].PreResponseBytes == 0 {
		t.Fatalf("request_bursts=%#v", report.RequestBursts)
	}
	if len(report.Onsets) != 1 || report.Onsets[0].NearbyRequests != 3 || report.Onsets[0].NearbyFailures != 1 || report.Onsets[0].PreCounts["jvm-multiline"] != 1 || report.Onsets[0].PreCounts["access"] != 3 || strings.Contains(strings.ToLower(report.Onsets[0].CorrelationStatement), " caused ") {
		t.Fatalf("onsets=%#v", report.Onsets)
	}
	if len(report.Buckets) != 3 {
		t.Fatalf("buckets=%#v", report.Buckets)
	}
}

func TestAnalyzeClampsWindowEndToLatestEvent(t *testing.T) {
	db := testDatabase(t)
	around, _ := time.Parse(time.RFC3339, "2026-08-05T12:03:31Z")
	options := QueryOptions{
		Around:            around,
		Before:            2 * time.Second,
		After:             6 * time.Hour,
		Bucket:            time.Second,
		QuietPeriod:       30 * time.Second,
		PreWindow:         2 * time.Second,
		CorrelationWindow: time.Second,
		Source:            "one",
	}
	report, err := Analyze(context.Background(), db, options)
	if err != nil {
		t.Fatal(err)
	}
	want := "2026-08-05T12:03:31.216Z"
	if report.WindowEnd != want {
		t.Fatalf("window_end=%q want=%q", report.WindowEnd, want)
	}
}

func TestAnalyzeRejectsAroundAfterLatestEvent(t *testing.T) {
	db := testDatabase(t)
	around, _ := time.Parse(time.RFC3339, "2026-08-05T12:03:40Z")
	options := QueryOptions{
		Around:            around,
		Before:            2 * time.Second,
		After:             2 * time.Second,
		Bucket:            time.Second,
		QuietPeriod:       30 * time.Second,
		PreWindow:         2 * time.Second,
		CorrelationWindow: time.Second,
		Source:            "one",
	}
	_, err := Analyze(context.Background(), db, options)
	if err == nil || !strings.Contains(err.Error(), "--around must not be after the latest available event") {
		t.Fatalf("err=%v", err)
	}
}

func TestAnalyzeTimelineFilters(t *testing.T) {
	event := Event{Family: "access", Severity: "SEVERE", Signature: "sig", RouteTemplate: "/route", Site: "site-a"}
	status := int64(500)
	event.Status = &status
	for name, options := range map[string]QueryOptions{"family": {Family: "access"}, "severity": {Severity: "SEVERE"}, "status-class": {Status: "5xx"}, "status-exact": {Status: "500"}, "route": {Route: "/route"}, "site": {Site: "site-a"}, "signature": {Signature: "sig"}} {
		if !matches(event, options) {
			t.Errorf("%s filter did not match", name)
		}
	}
	if matches(event, QueryOptions{Status: "404"}) || matches(event, QueryOptions{Family: "jvm-multiline"}) {
		t.Error("nonmatching filter matched")
	}
}

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"application.log": "Aug 05, 2026 7:03:30 AM example.Logger work\nINFO: protocol type: https\n",
		"access.log":      "10.0.0.1 [05/Aug/2026:07:03:28 -0500] port:1 \"GET /route?q=prior HTTP/1.1\" 200 180\n10.0.0.1 [05/Aug/2026:07:03:29 -0500] port:1 \"GET /route?q=prior HTTP/1.1\" 200 190\n10.0.0.1 [05/Aug/2026:07:03:30 -0500] port:1 \"GET /route?q=one HTTP/1.1\" 500 0\n10.0.0.1 [05/Aug/2026:07:03:30 -0500] port:1 \"GET /route?q=burst HTTP/1.1\" 200 512\n10.0.0.1 [05/Aug/2026:07:03:30 -0500] port:1 \"GET /route?q=burst HTTP/1.1\" 200 768\n",
		"catalina.log":    "05-Aug-2026 07:03:31.216 SEVERE [worker] example.Logger.work failed\n\tjava.lang.OutOfMemoryError: heap\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := storage.Open(filepath.Join(t.TempDir(), "clogs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, source := range []string{"one", "two"} {
		if _, err := ingest.Run(context.Background(), db, ingest.Options{Input: dir, Database: "", Source: source, Timezone: "America/Chicago", StoreRaw: true}); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func count(values []Count, name string) int64 {
	for _, value := range values {
		if value.Name == name {
			return value.Count
		}
	}
	return 0
}
