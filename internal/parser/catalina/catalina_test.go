package catalina

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FormlessEvoker/clogs/internal/model"
)

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

func TestParseMultilineConsecutiveAndEOF(t *testing.T) {
	t.Parallel()
	input := "orphan\n05-Aug-2026 07:03:31.216 SEVERE [worker-1] org.example.Handler.process Failed\n\tjava.lang.IllegalStateException: bad\n\tCaused by: java.lang.OutOfMemoryError: heap\n05-Aug-2026 07:03:32.001 SEVERE [worker-2] org.example.Other.work Again\n\tjava.lang.OutOfMemoryError: heap"
	location, _ := time.LoadLocation("America/Chicago")
	var events []model.Event
	result, err := Parse(context.Background(), strings.NewReader(input), Options{Timezone: location, SourcePath: "catalina.log"}, func(event model.Event) error { events = append(events, event); return nil })
	if err != nil || result.OrphanLines != 1 || len(events) != 2 {
		t.Fatalf("events=%d result=%#v err=%v", len(events), result, err)
	}
	first := events[0]
	if got := first.OccurredAt.Format(time.RFC3339Nano); got != "2026-08-05T12:03:31.216Z" {
		t.Errorf("time=%s", got)
	}
	if first.Precision != model.PrecisionMillisecond || first.Java.Thread != "worker-1" || first.Java.Operation != "process" || first.Java.ExceptionClass != "java.lang.IllegalStateException" || first.Java.RootCauseClass != "java.lang.OutOfMemoryError" {
		t.Errorf("event=%#v", first)
	}
	if events[1].SourceLineEnd != 6 || events[1].Java.ExceptionClass != "java.lang.OutOfMemoryError" {
		t.Errorf("last=%#v", events[1])
	}
}

func TestDatabaseTimeoutFixtureParses(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "incidents", "database-timeout", "catalina.log"))
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("America/Chicago")
	count := 0
	_, err = Parse(context.Background(), bytes.NewReader(content), Options{Timezone: location}, func(model.Event) error { count++; return nil })
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestDetectAndParseCRLF(t *testing.T) {
	t.Parallel()
	input := "prefix\r\n05-Aug-2026 07:03:31.216 SEVERE [worker] example.Logger.work failed\r\n\tjava.lang.IllegalStateException: bad\r\n"
	if got := Detect([]byte(input)); got != 100 {
		t.Fatalf("Detect=%d", got)
	}
	location, _ := time.LoadLocation("America/Chicago")
	var event model.Event
	result, err := Parse(context.Background(), strings.NewReader(input), Options{Timezone: location}, func(value model.Event) error {
		event = value
		return nil
	})
	if err != nil || result.OrphanLines != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if event.RawText != strings.TrimPrefix(input, "prefix\r\n") || event.Java.ExceptionMessage != "bad" || strings.Contains(event.Java.StackTrace, "\r") {
		t.Fatalf("event=%#v", event)
	}
	if got := Detect([]byte("not Catalina")); got != 0 {
		t.Fatalf("Detect=%d", got)
	}
}

func TestParsePropagatesCancellationReaderAndEmitterErrors(t *testing.T) {
	t.Parallel()
	location, _ := time.LoadLocation("America/Chicago")
	options := Options{Timezone: location}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, strings.NewReader(""), options, func(model.Event) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}

	readErr := errors.New("reader failed")
	input := io.MultiReader(strings.NewReader("05-Aug-2026 07:03:31.216 INFO [worker] example.Logger.work ok\n"), errorReader{err: readErr})
	if _, err := Parse(context.Background(), input, options, func(model.Event) error { return nil }); !errors.Is(err, readErr) || !strings.Contains(err.Error(), "read line 2") {
		t.Fatalf("reader error=%v", err)
	}

	emitErr := errors.New("emit failed")
	if _, err := Parse(context.Background(), strings.NewReader("05-Aug-2026 07:03:31.216 INFO [worker] example.Logger.work ok\n"), options, func(model.Event) error { return emitErr }); !errors.Is(err, emitErr) {
		t.Fatalf("emitter error=%v", err)
	}
}

func TestParseRejectsOversizedLines(t *testing.T) {
	t.Parallel()
	location, _ := time.LoadLocation("America/Chicago")
	_, err := Parse(context.Background(), strings.NewReader(strings.Repeat("x", MaxLineBytes+1)), Options{Timezone: location}, func(model.Event) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "line exceeds 1048576-byte limit") {
		t.Fatalf("error=%v", err)
	}
}

func TestParseTimestampOperationAndExceptionBoundaries(t *testing.T) {
	t.Parallel()
	if _, err := Parse(context.Background(), strings.NewReader(""), Options{}, func(model.Event) error { return nil }); err == nil || !strings.Contains(err.Error(), "requires an IANA timezone") {
		t.Fatalf("timezone error=%v", err)
	}
	location, _ := time.LoadLocation("America/Chicago")
	options := Options{Timezone: location}
	if _, err := Parse(context.Background(), strings.NewReader("32-Aug-2026 07:03:31.216 INFO [worker] Logger ok\n"), options, func(model.Event) error { return nil }); err == nil || !strings.Contains(err.Error(), "parse Catalina timestamp at line 1") {
		t.Fatalf("timestamp error=%v", err)
	}

	var event model.Event
	input := "05-Aug-2026 07:03:31.216 SEVERE [worker] Logger failed: java.lang.IllegalStateException: primary\n\tCaused by: java.lang.IllegalArgumentException: nested\n"
	_, err := Parse(context.Background(), strings.NewReader(input), options, func(value model.Event) error {
		event = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Java.Operation != "Logger" || event.Java.ExceptionClass != "java.lang.IllegalStateException" || event.Java.ExceptionMessage != "primary" || event.Java.RootCauseClass != "java.lang.IllegalArgumentException" || event.Java.RootCauseMessage != "nested" {
		t.Fatalf("java=%#v", event.Java)
	}
}

func TestSignatureUsesDocumentedInputs(t *testing.T) {
	t.Parallel()
	base := model.Event{Family: model.FamilyCatalina, Severity: "SEVERE", Message: "request  failed", SourcePath: "one", Java: &model.JavaDetails{Logger: "example.Logger", ExceptionClass: "java.lang.Error", RootCauseClass: "java.lang.IllegalStateException", RootCauseMessage: "root  cause"}}
	normalized := model.Event{Family: model.FamilyCatalina, Severity: "SEVERE", Message: "request failed", SourcePath: "two", Java: &model.JavaDetails{Logger: "example.Logger", Operation: "ignored", Thread: "ignored", ExceptionClass: "java.lang.Error", ExceptionMessage: "ignored", RootCauseClass: "java.lang.IllegalStateException", RootCauseMessage: "root cause"}}
	if signature(base) != signature(normalized) {
		t.Fatal("normalized or excluded fields changed signature")
	}
	changes := []model.Event{
		{Family: model.FamilyJVMMultiline, Severity: "SEVERE", Message: "request failed", Java: normalized.Java},
		{Family: model.FamilyCatalina, Severity: "INFO", Message: "request failed", Java: normalized.Java},
		{Family: model.FamilyCatalina, Severity: "SEVERE", Message: "different", Java: normalized.Java},
		{Family: model.FamilyCatalina, Severity: "SEVERE", Message: "request failed", Java: &model.JavaDetails{Logger: "other", ExceptionClass: "java.lang.Error", RootCauseClass: "java.lang.IllegalStateException", RootCauseMessage: "root cause"}},
		{Family: model.FamilyCatalina, Severity: "SEVERE", Message: "request failed", Java: &model.JavaDetails{Logger: "example.Logger", ExceptionClass: "java.lang.Exception", RootCauseClass: "java.lang.IllegalStateException", RootCauseMessage: "root cause"}},
		{Family: model.FamilyCatalina, Severity: "SEVERE", Message: "request failed", Java: &model.JavaDetails{Logger: "example.Logger", ExceptionClass: "java.lang.Error", RootCauseClass: "java.lang.Error", RootCauseMessage: "root cause"}},
		{Family: model.FamilyCatalina, Severity: "SEVERE", Message: "request failed", Java: &model.JavaDetails{Logger: "example.Logger", ExceptionClass: "java.lang.Error", RootCauseClass: "java.lang.IllegalStateException", RootCauseMessage: "different"}},
	}
	for index, changed := range changes {
		if signature(base) == signature(changed) {
			t.Errorf("change %d did not change signature", index)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add("05-Aug-2026 07:03:31.216 SEVERE [worker] example.Logger.work test\n\tjava.lang.Error: bad\n")
	location, _ := time.LoadLocation("America/Chicago")
	f.Fuzz(func(t *testing.T, input string) {
		_, _ = Parse(context.Background(), strings.NewReader(input), Options{Timezone: location}, func(model.Event) error { return nil })
	})
}
