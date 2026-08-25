package report

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/FormlessEvoker/clogs/internal/storage"
)

func TestAnalysisContractBucketsIncludeStartBoundariesAndFinalEnd(t *testing.T) {
	start := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	events := []Event{
		analysisEvent(start, "access", "at-start"),
		analysisEvent(start.Add(time.Second-time.Nanosecond), "jvm-multiline", "before-boundary"),
		analysisEvent(start.Add(time.Second), "access", "at-boundary"),
		analysisEvent(start.Add(2*time.Second), "catalina", "at-end"),
	}

	bucketValues := buckets(events, start, start.Add(2*time.Second), time.Second)
	if len(bucketValues) != 2 {
		t.Fatalf("bucket count=%d buckets=%#v", len(bucketValues), bucketValues)
	}
	if got, want := bucketValues[0].Counts, map[string]int{"access": 1, "jvm-multiline": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first bucket counts=%v want=%v", got, want)
	}
	if got, want := bucketValues[1].Counts, map[string]int{"access": 1, "catalina": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("final bucket counts=%v want=%v", got, want)
	}
	if values := buckets(events, start, start, time.Second); len(values) != 1 || values[0].Counts["access"] != 1 {
		t.Fatalf("zero-width window buckets=%#v", values)
	}
	if values := buckets(events, start, start.Add(time.Second), 0); values != nil {
		t.Fatalf("non-positive bucket size values=%#v", values)
	}
}

func TestAnalysisContractRoutesAndSignaturesCountBaselineAndBreakTies(t *testing.T) {
	status200, status500, status503 := int64(200), int64(500), int64(503)
	events := []Event{
		{RouteTemplate: "/z", Site: "a", Status: &status500, Signature: "z"},
		{RouteTemplate: "/a", Site: "a", Status: &status503, Signature: "a"},
		{RouteTemplate: "/a", Site: "a", Status: &status200, Signature: "z"},
		{RouteTemplate: "/a", Site: "b", Status: &status500, Signature: "b"},
		{RouteTemplate: "/ignored", Signature: "a"},
	}

	routes := routeRates(events)
	if got, want := routes, []RouteRate{{Route: "/a", Site: "a", Requests: 2, Failures: 1, FailureRate: .5}, {Route: "/a", Site: "b", Requests: 1, Failures: 1, FailureRate: 1}, {Route: "/z", Site: "a", Requests: 1, Failures: 1, FailureRate: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routes=%#v want=%#v", got, want)
	}
	if got, want := signatureCounts(events), []Count{{Name: "a", Count: 2}, {Name: "z", Count: 2}, {Name: "b", Count: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("signatures=%#v want=%#v", got, want)
	}
}

func TestAnalysisContractQuietPreOnsetAndCorrelationBoundaries(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	status200, status500 := int64(200), int64(500)
	events := []Event{
		analysisEvent(base, "access", "outside-pre"),
		analysisEvent(base.Add(2*time.Second), "access", "at-pre-start"),
		{OccurredAtNS: base.Add(2 * time.Second).UnixNano(), OccurredAt: base.Add(2 * time.Second).Format(time.RFC3339Nano), Family: "access", Signature: "request-before", Status: &status500},
		analysisEvent(base.Add(3*time.Second), "access", "inside-pre"),
		analysisEvent(base.Add(4*time.Second), "access", "at-onset"),
		{OccurredAtNS: base.Add(4 * time.Second).UnixNano(), OccurredAt: base.Add(4 * time.Second).Format(time.RFC3339Nano), Family: "catalina", Severity: "SEVERE", Signature: "oom", ExceptionClass: "java.lang.OutOfMemoryError"},
		{OccurredAtNS: base.Add(5 * time.Second).UnixNano(), OccurredAt: base.Add(5 * time.Second).Format(time.RFC3339Nano), Family: "catalina", Severity: "SEVERE", Signature: "oom", ExceptionClass: "java.lang.OutOfMemoryError"},
		{OccurredAtNS: base.Add(6 * time.Second).UnixNano(), OccurredAt: base.Add(6 * time.Second).Format(time.RFC3339Nano), Family: "access", Signature: "request-at-positive-edge", Status: &status200},
		{OccurredAtNS: base.Add(6 * time.Second).UnixNano(), OccurredAt: base.Add(6 * time.Second).Format(time.RFC3339Nano), Family: "catalina", Severity: "SEVERE", Signature: "oom", ExceptionClass: "java.lang.OutOfMemoryError"},
		{OccurredAtNS: base.Add(7 * time.Second).UnixNano(), OccurredAt: base.Add(7 * time.Second).Format(time.RFC3339Nano), Family: "catalina", Severity: "SEVERE", Signature: "oom", ExceptionClass: "java.lang.OutOfMemoryError"},
		{OccurredAtNS: base.Add(9 * time.Second).UnixNano(), OccurredAt: base.Add(9 * time.Second).Format(time.RFC3339Nano), Family: "catalina", Severity: "SEVERE", Signature: "oom", ExceptionClass: "java.lang.OutOfMemoryError"},
		{OccurredAtNS: base.Add(11 * time.Second).UnixNano(), OccurredAt: base.Add(11 * time.Second).Format(time.RFC3339Nano), Family: "catalina", Severity: "SEVERE", Signature: "oom", ExceptionClass: "java.lang.OutOfMemoryError"},
	}
	options := QueryOptions{QuietPeriod: 2 * time.Second, PreWindow: 2 * time.Second, CorrelationWindow: 2 * time.Second}

	onsetValues := onsets(events, options)
	if len(onsetValues) != 3 {
		t.Fatalf("onsets=%#v", onsetValues)
	}
	first := onsetValues[0]
	if first.OccurredAt != base.Add(4*time.Second).Format(time.RFC3339Nano) || first.PreCounts["access"] != 4 || first.NearbyRequests != 2 || first.NearbyFailures != 1 {
		t.Fatalf("first onset=%#v", first)
	}
	if got, want := first.PreSignatures, []Count{{Name: "at-onset", Count: 1}, {Name: "at-pre-start", Count: 1}, {Name: "inside-pre", Count: 1}, {Name: "request-before", Count: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pre-signatures=%#v want=%#v", got, want)
	}
	if !strings.Contains(first.CorrelationStatement, "temporal proximity does not establish causation") {
		t.Fatalf("correlation statement=%q", first.CorrelationStatement)
	}
	if got, want := []string{onsetValues[1].OccurredAt, onsetValues[2].OccurredAt}, []string{base.Add(9 * time.Second).Format(time.RFC3339Nano), base.Add(11 * time.Second).Format(time.RFC3339Nano)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("quiet-period onset times=%#v want=%#v", got, want)
	}
}

func TestAnalysisContractAnalyzeQuietPeriodEligibilityAndSignatureIsolation(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		events []analysisOnsetEvent
		want   []string
	}{
		{
			name: "same signature within quiet period is suppressed",
			events: []analysisOnsetEvent{
				{at: base, family: "jvm-multiline", severity: "ERROR", signature: "same"},
				{at: base.Add(time.Second), family: "jvm-multiline", severity: "ERROR", signature: "same"},
			},
			want: []string{"same"},
		},
		{
			name: "different signatures have independent quiet periods",
			events: []analysisOnsetEvent{
				{at: base, family: "jvm-multiline", severity: "ERROR", signature: "first"},
				{at: base.Add(time.Second), family: "jvm-multiline", severity: "ERROR", signature: "second"},
			},
			want: []string{"first", "second"},
		},
		{
			name: "JVM ERROR and SEVERE are eligible",
			events: []analysisOnsetEvent{
				{at: base, family: "jvm-multiline", severity: "ERROR", signature: "error"},
				{at: base.Add(time.Second), family: "jvm-multiline", severity: "SEVERE", signature: "severe"},
			},
			want: []string{"error", "severe"},
		},
		{
			name: "non-error JVM event is not an onset",
			events: []analysisOnsetEvent{
				{at: base, family: "jvm-multiline", severity: "INFO", signature: "info"},
			},
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := analysisOnsetDatabase(t, test.events)
			report, err := Analyze(context.Background(), db, QueryOptions{
				Around: base, Before: time.Second, After: time.Hour, Bucket: time.Second, QuietPeriod: 10 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, onset := range report.Onsets {
				got = append(got, onset.Signature)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("onset signatures=%q want=%q", got, test.want)
			}
		})
	}
}

func TestAnalysisContractPressureWindowsAreHalfOpenAndSorted(t *testing.T) {
	base := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	status := int64(200)
	bytes10, bytes20, bytes30 := int64(10), int64(20), int64(30)
	events := []Event{
		{OccurredAtNS: base.UnixNano(), RouteTemplate: "/a", Status: &status, ResponseBytes: &bytes10},
		{OccurredAtNS: base.Add(time.Second).UnixNano(), RouteTemplate: "/z", Status: &status, ResponseBytes: &bytes20},
		{OccurredAtNS: base.Add(2 * time.Second).UnixNano(), RouteTemplate: "/a", Status: &status, ResponseBytes: &bytes30},
		{OccurredAtNS: base.Add(3 * time.Second).UnixNano(), Family: "catalina", ExceptionClass: "java.lang.OutOfMemoryError"},
	}
	values := requestSignals(events, QueryOptions{PreWindow: time.Second})
	if got, want := values, []RequestSignal{
		{Route: "/a", OnsetCount: 1, PreRequests: 1, PreResponseBytes: 30, PreToPrior: 1, BytesPreToPrior: 30},
		{Route: "/z", PriorRequests: 1, PriorResponseBytes: 20, BytesPreToPrior: 0},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("request signals=%#v want=%#v", got, want)
	}
	if got, want := requestBursts(events, QueryOptions{PreWindow: time.Second}), []RequestBurst{
		{Fingerprint: "/a", OnsetCount: 1, PreRequests: 1, PreResponseBytes: 30, PreToPrior: 1, BytesPreToPrior: 30},
		{Fingerprint: "/z", PriorRequests: 1, PriorResponseBytes: 20, BytesPreToPrior: 0},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("request bursts=%#v want=%#v", got, want)
	}
}

func TestAnalysisContractBaselineIsTimeSourceOnlyAndTimelineIsFiltered(t *testing.T) {
	db := testDatabase(t)
	around := time.Date(2026, time.August, 5, 12, 3, 31, 0, time.UTC)
	report, err := Analyze(context.Background(), db, QueryOptions{
		Around: around, Before: 2 * time.Second, After: 2 * time.Second, Bucket: time.Second,
		QuietPeriod: 30 * time.Second, PreWindow: 2 * time.Second, CorrelationWindow: 2 * time.Second,
		Source: "one", Status: "500",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.BaselineSize != 6 || report.SampleSize != 1 || len(report.Timeline) != 1 || report.Timeline[0].Status == nil || *report.Timeline[0].Status != 500 {
		t.Fatalf("baseline/timeline=%#v", report)
	}
	if report.Filters["source"] != "one" || report.Filters["status"] != "500" || len(report.Filters) != 2 {
		t.Fatalf("filters=%#v", report.Filters)
	}
	if len(report.Routes) != 1 || report.Routes[0].Requests != 4 || report.Routes[0].Failures != 1 || len(report.Buckets) != 3 {
		t.Fatalf("derived values should use unfiltered baseline: %#v", report)
	}
}

func TestAnalysisContractClampsAndAcceptsCenterAtLatestEvent(t *testing.T) {
	db := testDatabase(t)
	latest := time.Date(2026, time.August, 5, 12, 3, 31, 216000000, time.UTC)
	report, err := Analyze(context.Background(), db, QueryOptions{Around: latest, After: time.Hour, Bucket: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if report.WindowStart != latest.Format(time.RFC3339Nano) || report.WindowEnd != latest.Format(time.RFC3339Nano) {
		t.Fatalf("clamped window=%s..%s", report.WindowStart, report.WindowEnd)
	}
	_, err = Analyze(context.Background(), db, QueryOptions{Around: latest.Add(time.Nanosecond), Bucket: time.Second})
	if err == nil || !strings.Contains(err.Error(), "--around must not be after the latest available event") {
		t.Fatalf("err=%v", err)
	}
}

func TestAnalysisContractTimelineTieOrderUsesEventOrder(t *testing.T) {
	db := orderedExportDatabase(t)
	center := time.Unix(0, 100).UTC()
	report, err := Analyze(context.Background(), db, QueryOptions{Around: center, Bucket: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha-z", exportSpecialMessage, "beta-a-second", "beta-a-next-line", "beta-z"}
	if len(report.Timeline) != len(want) {
		t.Fatalf("timeline=%#v", report.Timeline)
	}
	for i, message := range want {
		if report.Timeline[i].Message != message {
			t.Fatalf("timeline[%d].Message=%q want %q", i, report.Timeline[i].Message, message)
		}
	}
}

func analysisEvent(at time.Time, family, signature string) Event {
	return Event{OccurredAtNS: at.UnixNano(), OccurredAt: at.Format(time.RFC3339Nano), Family: family, Signature: signature}
}

type analysisOnsetEvent struct {
	at                          time.Time
	family, severity, signature string
}

func analysisOnsetDatabase(t *testing.T, events []analysisOnsetEvent) *sql.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "analysis.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	result, err := db.Exec(`INSERT INTO ingest_runs(started_at,input_path,source_label,status) VALUES(?,?,?,?)`, "2026-08-05T12:00:00Z", "analysis", "analysis", "complete")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	result, err = db.Exec(`INSERT INTO source_files(ingest_run_id,source_label,path,relative_path,sha256,size_bytes,detected_family,parser_version,ingested_at) VALUES(?,?,?,?,?,?,?,?,?)`, runID, "analysis", "analysis.log", "analysis.log", "analysis", 1, "jvm-multiline", "test", "2026-08-05T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	sourceID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	signatureIDs := map[string]int64{}
	for index, event := range events {
		signatureID, exists := signatureIDs[event.signature]
		if !exists {
			result, err := db.Exec(`INSERT INTO signatures(fingerprint,algorithm_version,family) VALUES(?,?,?)`, event.signature, 1, event.family)
			if err != nil {
				t.Fatal(err)
			}
			signatureID, err = result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			signatureIDs[event.signature] = signatureID
		}
		at := event.at.UTC()
		_, err = db.Exec(`INSERT INTO events(source_file_id,signature_id,family,occurred_at_ns,occurred_at_utc,original_timestamp,timestamp_precision,source_line_start,source_line_end,source_ordinal,severity,message) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, sourceID, signatureID, event.family, at.UnixNano(), at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano), "nanosecond", index+1, index+1, index+1, event.severity, event.signature)
		if err != nil {
			t.Fatal(err)
		}
	}
	return db
}
