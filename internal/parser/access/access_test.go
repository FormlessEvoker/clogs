package access

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FormlessEvoker/clogs/internal/model"
)

// testRouteTemplates mirrors a typical configured route shape so tests cover
// placeholder substitution and site capture.
var testRouteTemplates = []string{"/svc/v4/api/site/{site}"}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

func TestParseExtractsAndNormalizesAccessEvent(t *testing.T) {
	t.Parallel()
	input := "192.0.2.143 [05/Aug/2026:07:03:31 -0500] port:15077 \"GET /svc/v4/api/site/north/netconfig/NetConfig?forcelock=false HTTP/1.1\" 500 -\n[2001:db8::1] [05/Aug/2026:07:03:32 -0500] port:15077 \"POST /other?q=yes HTTP/1.1\" 302 42\n"
	var events []model.Event
	result, err := Parse(context.Background(), strings.NewReader(input), Options{SourcePath: "access.log", RouteTemplates: testRouteTemplates}, func(event model.Event) error { events = append(events, event); return nil })
	if err != nil || len(result.Malformed) != 0 || len(events) != 2 {
		t.Fatalf("events=%d result=%#v err=%v", len(events), result, err)
	}
	first := events[0]
	if got, want := first.OccurredAt.Format("2006-01-02T15:04:05Z07:00"), "2026-08-05T12:03:31Z"; got != want {
		t.Errorf("occurred_at=%s", got)
	}
	if first.HTTP.Site != "north" || first.HTTP.RouteTemplate != "/svc/v4/api/site/{site}/netconfig/NetConfig" || first.HTTP.RawQuery != "forcelock=false" || first.HTTP.ResponseBytes != nil {
		t.Errorf("http=%#v", first.HTTP)
	}
	if events[1].HTTP.ClientAddress != "[2001:db8::1]" || events[1].HTTP.ResponseBytes == nil || *events[1].HTTP.ResponseBytes != 42 {
		t.Errorf("ipv6=%#v", events[1].HTTP)
	}
}

func TestSignatureExcludesQueryAndClient(t *testing.T) {
	t.Parallel()
	first, err := parseLine("10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /svc/v4/api/site/a/x?q=one HTTP/1.1\" 500 1", "a", 1, testRouteTemplates)
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseLine("10.0.0.2 [05/Aug/2026:07:03:32 -0500] port:1 \"GET /svc/v4/api/site/b/x?q=two HTTP/1.1\" 500 2", "b", 2, testRouteTemplates)
	if err != nil {
		t.Fatal(err)
	}
	if first.Signature != second.Signature {
		t.Errorf("signature differs: %s %s", first.Signature, second.Signature)
	}
	third, _ := parseLine("10.0.0.2 [05/Aug/2026:07:03:32 -0500] port:1 \"POST /svc/v4/api/site/b/x HTTP/1.1\" 500 2", "a", 2, testRouteTemplates)
	if first.Signature == third.Signature {
		t.Error("method change did not change signature")
	}
	fourth, _ := parseLine("10.0.0.2 [05/Aug/2026:07:03:32 -0500] port:1 \"GET /different HTTP/1.1\" 500 2", "a", 2, testRouteTemplates)
	if first.Signature == fourth.Signature {
		t.Error("route change did not change signature")
	}
	fifth, _ := parseLine("10.0.0.2 [05/Aug/2026:07:03:32 -0500] port:1 \"GET /svc/v4/api/site/b/x HTTP/1.1\" 404 2", "a", 2, testRouteTemplates)
	if first.Signature == fifth.Signature {
		t.Error("status change did not change signature")
	}
}

func TestParseRetainsValidLinesAndReportsMalformed(t *testing.T) {
	t.Parallel()
	input := "not an access line\n10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 0\n"
	count := 0
	result, err := Parse(context.Background(), strings.NewReader(input), Options{}, func(model.Event) error { count++; return nil })
	if err != nil || count != 1 || len(result.Malformed) != 1 || result.Malformed[0].Line != 1 {
		t.Fatalf("count=%d result=%#v err=%v", count, result, err)
	}
}

func TestDatabaseTimeoutFixtureHasThreeRequestsAndOneServerError(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "incidents", "database-timeout", "gateway-access.log"))
	if err != nil {
		t.Fatal(err)
	}
	requests, errors := 0, 0
	_, err = Parse(context.Background(), bytes.NewReader(content), Options{}, func(event model.Event) error {
		requests++
		if event.HTTP.Status == 500 {
			errors++
		}
		return nil
	})
	if err != nil || requests != 3 || errors != 1 {
		t.Fatalf("requests=%d errors=%d err=%v", requests, errors, err)
	}
}

func TestDetectAndParseCRLFBlankAndInvalidURI(t *testing.T) {
	t.Parallel()
	line := "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET % HTTP/1.1\" 200 0"
	if got := Detect([]byte("blank\r\n" + line + "\r\n")); got != 100 {
		t.Fatalf("Detect=%d", got)
	}
	var events []model.Event
	result, err := Parse(context.Background(), strings.NewReader("\r\n"+line+"\r\n"), Options{}, func(event model.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil || len(result.Malformed) != 0 || len(events) != 1 {
		t.Fatalf("events=%d result=%#v err=%v", len(events), result, err)
	}
	if events[0].HTTP.Path != "" || events[0].HTTP.RawQuery != "" || events[0].HTTP.RouteTemplate != "" || events[0].RawText != line+"\n" {
		t.Fatalf("event=%#v", events[0])
	}
	if got := Detect([]byte("not access")); got != 0 {
		t.Fatalf("Detect=%d", got)
	}
}

func TestParsePropagatesCancellationReaderAndEmitterErrors(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, strings.NewReader(""), Options{}, func(model.Event) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}

	readErr := errors.New("reader failed")
	if _, err := Parse(context.Background(), errorReader{err: readErr}, Options{}, func(model.Event) error { return nil }); !errors.Is(err, readErr) || !strings.Contains(err.Error(), "read line 1") {
		t.Fatalf("reader error=%v", err)
	}

	emitErr := errors.New("emit failed")
	input := "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 0\n"
	if _, err := Parse(context.Background(), strings.NewReader(input), Options{}, func(model.Event) error { return emitErr }); !errors.Is(err, emitErr) {
		t.Fatalf("emitter error=%v", err)
	}
}

func TestParseRejectsOversizedLines(t *testing.T) {
	t.Parallel()
	_, err := Parse(context.Background(), strings.NewReader(strings.Repeat("x", MaxLineBytes+1)), Options{}, func(model.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "line exceeds 1048576-byte limit") {
		t.Fatalf("error=%v", err)
	}
}

func TestParseReportsMalformedFieldVariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "timestamp", line: "10.0.0.1 [32/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 0", want: "invalid timestamp"},
		{name: "port overflow", line: "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:999999999999999999999999999 \"GET /ok HTTP/1.1\" 200 0", want: "invalid port"},
		{name: "request", line: "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok\" 200 0", want: "invalid quoted request"},
		{name: "status shape", line: "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 20 0", want: "does not match access-log format"},
		{name: "bytes text", line: "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 nope", want: "invalid response bytes"},
		{name: "bytes negative", line: "10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 -1", want: "invalid response bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Parse(context.Background(), strings.NewReader(test.line+"\n"), Options{}, func(model.Event) error { return nil })
			if err != nil || len(result.Malformed) != 1 || !strings.Contains(result.Malformed[0].Error, test.want) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	f.Add("10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 0\n")
	f.Fuzz(func(t *testing.T, input string) {
		_, _ = Parse(context.Background(), strings.NewReader(input), Options{}, func(model.Event) error { return nil })
	})
}
