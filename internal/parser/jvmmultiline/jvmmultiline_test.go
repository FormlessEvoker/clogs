package jvmmultiline

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/FormlessEvoker/clogs/internal/model"
)

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

func TestParseMultilineEventAndEOF(t *testing.T) {
	t.Parallel()
	input := "orphan text\nAug 05, 2026 6:27:33 AM com.example.ProtocolWorker buildProtocolConfig\nINFO: protocol type: https\njava.lang.IllegalStateException: broken\nCaused by: java.lang.OutOfMemoryError: exhausted\nAug 05, 2026 6:27:34 AM com.example.Other work\nWARNING: done"
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	var events []model.Event
	result, err := Parse(context.Background(), strings.NewReader(input), Options{Timezone: location, SourcePath: "fixture.log"}, func(event model.Event) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.OrphanLines != 1 {
		t.Errorf("orphan lines = %d, want 1", result.OrphanLines)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	first := events[0]
	if got, want := first.OccurredAt.Format(time.RFC3339), "2026-08-05T11:27:33Z"; got != want {
		t.Errorf("time = %q, want %q", got, want)
	}
	if first.SourceLineStart != 2 || first.SourceLineEnd != 5 {
		t.Errorf("line range = %d-%d", first.SourceLineStart, first.SourceLineEnd)
	}
	if first.Java.Logger != "com.example.ProtocolWorker" || first.Java.Operation != "buildProtocolConfig" {
		t.Errorf("java = %#v", first.Java)
	}
	if first.Java.ProtocolType != "https" || first.Java.ExceptionClass != "java.lang.IllegalStateException" || first.Java.RootCauseClass != "java.lang.OutOfMemoryError" {
		t.Errorf("java details = %#v", first.Java)
	}
	if first.Signature == "" || !strings.Contains(first.RawText, "Caused by:") {
		t.Errorf("event = %#v", first)
	}
	if got, want := events[1].SourceLineEnd, int64(7); got != want {
		t.Errorf("EOF line end = %d, want %d", got, want)
	}
}

func TestParseWarnsWhenLevelIsMissing(t *testing.T) {
	t.Parallel()
	location, _ := time.LoadLocation("America/Chicago")
	var event model.Event
	_, err := Parse(context.Background(), strings.NewReader("Aug 05, 2026 6:27:33 AM example.Logger work\nmessage without level\n"), Options{Timezone: location}, func(value model.Event) error { event = value; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(event.ParseWarnings) != 1 || event.Severity != "" {
		t.Errorf("event = %#v", event)
	}
}

func TestDetect(t *testing.T) {
	t.Parallel()
	if got := Detect([]byte("prefix\r\nAug 05, 2026 6:27:33 AM example.Logger work\r\n")); got != 100 {
		t.Errorf("Detect = %d", got)
	}
	if got := Detect([]byte("not a JVM log")); got != 0 {
		t.Errorf("Detect = %d", got)
	}
}

func TestParseAcceptsCRLF(t *testing.T) {
	t.Parallel()
	location, _ := time.LoadLocation("America/Chicago")
	var event model.Event
	input := "Aug 05, 2026 6:27:33 AM example.Logger work\r\nINFO: ok\r\n"
	result, err := Parse(context.Background(), strings.NewReader(input), Options{Timezone: location}, func(value model.Event) error {
		event = value
		return nil
	})
	if err != nil || result.OrphanLines != 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if event.Message != "ok" || event.RawText != input || event.SourceLineEnd != 2 {
		t.Fatalf("event=%#v", event)
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
	input := io.MultiReader(strings.NewReader("Aug 05, 2026 6:27:33 AM example.Logger work\n"), errorReader{err: readErr})
	if _, err := Parse(context.Background(), input, options, func(model.Event) error { return nil }); !errors.Is(err, readErr) || !strings.Contains(err.Error(), "read line 2") {
		t.Fatalf("reader error=%v", err)
	}

	emitErr := errors.New("emit failed")
	if _, err := Parse(context.Background(), strings.NewReader("Aug 05, 2026 6:27:33 AM example.Logger work\nINFO: ok\n"), options, func(model.Event) error { return emitErr }); !errors.Is(err, emitErr) {
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

func TestParseTimestampAndContextBoundaries(t *testing.T) {
	t.Parallel()
	if _, err := Parse(context.Background(), strings.NewReader(""), Options{}, func(model.Event) error { return nil }); err == nil || !strings.Contains(err.Error(), "requires an IANA timezone") {
		t.Fatalf("timezone error=%v", err)
	}
	location, _ := time.LoadLocation("America/Chicago")
	options := Options{Timezone: location}
	if _, err := Parse(context.Background(), strings.NewReader("Aug 32, 2026 6:27:33 AM example.Logger work\nINFO: bad\n"), options, func(model.Event) error { return nil }); err == nil || !strings.Contains(err.Error(), "parse JVM timestamp at line 1") {
		t.Fatalf("timestamp error=%v", err)
	}

	var event model.Event
	result, err := Parse(context.Background(), strings.NewReader("Aug 05, 2026 6:27:33 AM LoggerOnly\nINFO: ok\n"), options, func(value model.Event) error {
		event = value
		return nil
	})
	if err != nil || result.OrphanLines != 0 || event.Java.Logger != "LoggerOnly" || event.Java.Operation != "" {
		t.Fatalf("event=%#v result=%#v err=%v", event, result, err)
	}

	result, err = Parse(context.Background(), strings.NewReader("Aug 05, 2026 6:27:33 AM\n"), options, func(model.Event) error { return nil })
	if err != nil || result.OrphanLines != 1 {
		t.Fatalf("missing-context result=%#v err=%v", result, err)
	}
}

func TestSignatureUsesDocumentedInputs(t *testing.T) {
	t.Parallel()
	base := model.Event{Family: model.FamilyJVMMultiline, Severity: "INFO", Message: "request  failed", SourcePath: "one", Java: &model.JavaDetails{Logger: "example.Logger", Operation: "work"}}
	normalized := model.Event{Family: model.FamilyJVMMultiline, Severity: "INFO", Message: "request failed", SourcePath: "two", Java: &model.JavaDetails{Logger: "example.Logger", Operation: "work"}}
	if signature(base) != signature(normalized) {
		t.Fatal("whitespace or excluded source path changed signature")
	}
	changes := []model.Event{
		{Family: model.FamilyCatalina, Severity: "INFO", Message: "request failed", Java: &model.JavaDetails{Logger: "example.Logger", Operation: "work"}},
		{Family: model.FamilyJVMMultiline, Severity: "WARN", Message: "request failed", Java: &model.JavaDetails{Logger: "example.Logger", Operation: "work"}},
		{Family: model.FamilyJVMMultiline, Severity: "INFO", Message: "request failed", Java: &model.JavaDetails{Logger: "other.Logger", Operation: "work"}},
		{Family: model.FamilyJVMMultiline, Severity: "INFO", Message: "request failed", Java: &model.JavaDetails{Logger: "example.Logger", Operation: "other"}},
		{Family: model.FamilyJVMMultiline, Severity: "INFO", Message: "different", Java: &model.JavaDetails{Logger: "example.Logger", Operation: "work"}},
	}
	for index, changed := range changes {
		if signature(base) == signature(changed) {
			t.Errorf("change %d did not change signature", index)
		}
	}
}

func FuzzDetect(f *testing.F) {
	f.Add([]byte("Aug 05, 2026 6:27:33 AM example.Logger work\n"))
	f.Add([]byte("arbitrary\x00bytes"))
	f.Fuzz(func(t *testing.T, input []byte) { _ = Detect(input) })
}

func FuzzParse(f *testing.F) {
	f.Add("Aug 05, 2026 6:27:33 AM example.Logger work\nINFO: test\n")
	f.Add("\x00\nnot a header")
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, input string) {
		_, _ = Parse(context.Background(), strings.NewReader(input), Options{Timezone: location}, func(model.Event) error { return nil })
	})
}
