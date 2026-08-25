package report

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWriteIncidentHTMLIsDeterministicAndEscapesEvidence(t *testing.T) {
	start := time.Date(2026, 8, 7, 1, 30, 0, 0, time.UTC)
	status := int64(500)
	analysis := QueryReport{
		WindowStart: start.Format(time.RFC3339Nano), WindowEnd: start.Add(2 * time.Minute).Format(time.RFC3339Nano), Around: start.Add(time.Minute).Format(time.RFC3339Nano),
		Filters: map[string]string{"source": `server<script>alert(1)</script>`}, Bucket: "1m", CorrelationWindow: "2s", BaselineSize: 2,
		Timeline: []Event{
			{Family: "access", OccurredAtNS: start.Add(30 * time.Second).UnixNano(), OccurredAt: start.Add(30 * time.Second).Format(time.RFC3339Nano), Status: &status, Method: "GET", RouteTemplate: `/unsafe</span><script>alert(2)</script>`, Site: "site-a", Message: `</pre><script>alert(3)</script>`, SourcePath: `bad"><img src=x onerror=alert(4)>`, SourceLineStart: 7},
			{Family: "catalina", OccurredAtNS: start.Add(30*time.Second + 200*time.Millisecond).UnixNano(), OccurredAt: start.Add(30*time.Second + 200*time.Millisecond).Format(time.RFC3339Nano), ExceptionClass: "java.lang.OutOfMemoryError", Message: "Failed: <heap>", SourcePath: "catalina.log", SourceLineStart: 9},
		},
		Onsets: []Onset{{OccurredAt: start.Add(30*time.Second + 200*time.Millisecond).Format(time.RFC3339Nano), ExceptionClass: "java.lang.OutOfMemoryError"}},
	}
	var first, second bytes.Buffer
	config := IncidentHTMLConfig{Timezone: "America/New_York", Title: `<script>alert("title")</script>`}
	if err := WriteIncidentHTML(&first, analysis, config); err != nil {
		t.Fatal(err)
	}
	if err := WriteIncidentHTML(&second, analysis, config); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("incident HTML is not deterministic")
	}
	output := first.String()
	for _, want := range []string{"Unified incident timeline", "Route impact", "Pre-OOM route pressure", "Repeated exact calls", "OOM-to-failure proximity", "Affected sites over time", "Evidence sequence", "1 / 1"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(output, "<script>") || strings.Contains(output, "<img src=x") || !strings.Contains(output, "&lt;script&gt;") {
		t.Fatalf("log-derived HTML was not escaped: %s", output)
	}
}

func TestWriteIncidentHTMLHandlesNoIncidentEvidence(t *testing.T) {
	start := time.Date(2026, 8, 7, 1, 30, 0, 0, time.UTC)
	analysis := QueryReport{WindowStart: start.Format(time.RFC3339Nano), WindowEnd: start.Add(time.Minute).Format(time.RFC3339Nano), Around: start.Format(time.RFC3339Nano), Filters: map[string]string{}, Bucket: "30s", CorrelationWindow: "2s"}
	var output bytes.Buffer
	if err := WriteIncidentHTML(&output, analysis, IncidentHTMLConfig{Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No HTTP failures") || !strings.Contains(output.String(), "No incident evidence") {
		t.Fatalf("empty state missing: %s", output.String())
	}
}

func TestWriteIncidentHTMLRejectsExcessiveBuckets(t *testing.T) {
	start := time.Date(2026, 8, 7, 1, 30, 0, 0, time.UTC)
	analysis := QueryReport{WindowStart: start.Format(time.RFC3339Nano), WindowEnd: start.Add(time.Hour).Format(time.RFC3339Nano), Around: start.Format(time.RFC3339Nano), Filters: map[string]string{}, Bucket: "1s", CorrelationWindow: "2s"}
	if err := WriteIncidentHTML(&bytes.Buffer{}, analysis, IncidentHTMLConfig{Timezone: "UTC"}); err == nil || !strings.Contains(err.Error(), "increase --bucket") {
		t.Fatalf("error=%v", err)
	}
}
