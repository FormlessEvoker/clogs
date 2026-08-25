package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunIngestInfersLatestCollectionAndSourceFromServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clogs.yml"), []byte(strings.TrimSpace(`
defaults:
  timezone: America/Chicago
remote:
  defaults:
    out: ./downloads
  servers:
    app-01.example.test:
      timezone: America/New_York
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCollection := filepath.Join(root, "downloads", "app-01.example.test", "2026-08-10T120000Z")
	newCollection := filepath.Join(root, "downloads", "app-01.example.test", "2026-08-11T120000Z")
	if err := os.MkdirAll(oldCollection, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newCollection, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldCollection, "application.log"), []byte("not a log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newCollection, "application.log"), []byte("Aug 11, 2026 1:15:00 PM example.Logger work\nINFO: complete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", "--server", "app-01.example.test"}, &stdout, &stderr)
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	wantDB := filepath.Join(newCollection, "app-01.example.test.db")
	if _, err := os.Stat(wantDB); err != nil {
		t.Fatalf("expected db %q: %v", wantDB, err)
	}
	if !strings.Contains(stdout.String(), "Source:           app-01.example.test") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunIngestServerUsesConfiguredDBRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clogs.yml"), []byte(strings.TrimSpace(`
defaults:
  timezone: America/Chicago
paths:
  downloads_root: ./downloads
  db_root: ./data/db
remote:
  servers:
    app-01.example.test:
      timezone: America/New_York
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collection := filepath.Join(root, "downloads", "app-01.example.test", "2026-08-11T120000Z")
	if err := os.MkdirAll(collection, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection, "application.log"), []byte("Aug 11, 2026 1:15:00 PM example.Logger work\nINFO: complete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", "--server", "app-01.example.test"}, &stdout, &stderr)
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	wantDB := filepath.Join(root, "data", "db", "app-01.example.test", "2026-08-11T120000Z", "app-01.example.test.db")
	if _, err := os.Stat(wantDB); err != nil {
		t.Fatalf("expected db %q: %v", wantDB, err)
	}
}

func TestRunIngestMovesInferredCollectionToSourceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clogs.yml"), []byte(strings.TrimSpace(`
defaults:
  timezone: America/Chicago
paths:
  downloads_root: ./downloads
  db_root: ./data/db
  source_root: ./data/source
remote:
  servers:
    app-01.example.test:
      timezone: America/New_York
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collection := filepath.Join(root, "downloads", "app-01.example.test", "2026-08-11T120000Z")
	if err := os.MkdirAll(collection, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection, "application.log"), []byte("Aug 11, 2026 1:15:00 PM example.Logger work\nINFO: complete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", "--server", "app-01.example.test"}, &stdout, &stderr)
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	archived := filepath.Join(root, "data", "source", "app-01.example.test", "2026-08-11T120000Z")
	if _, err := os.Stat(filepath.Join(archived, "application.log")); err != nil {
		t.Fatalf("expected archived source file: %v", err)
	}
	if _, err := os.Stat(collection); !os.IsNotExist(err) {
		t.Fatalf("expected original collection to move, stat err=%v", err)
	}
}

func TestRunQueryInfersDatabaseFromServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clogs.yml"), []byte(strings.TrimSpace(`
defaults:
  timezone: America/Chicago
remote:
  defaults:
    out: ./downloads
  servers:
    app-01.example.test:
      timezone: America/New_York
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collection := filepath.Join(root, "downloads", "app-01.example.test", "2026-08-11T120000Z")
	if err := os.MkdirAll(collection, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection, "application.log"), []byte("Aug 11, 2026 1:15:00 PM example.Logger work\nINFO: complete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var ingestOut, ingestErr bytes.Buffer
	if code := Run([]string{"ingest", "--server", "app-01.example.test"}, &ingestOut, &ingestErr); code != ExitSuccess || ingestErr.Len() != 0 {
		t.Fatalf("ingest code=%d stdout=%q stderr=%q", code, ingestOut.String(), ingestErr.String())
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"query", "--server", "app-01.example.test", "--around", "2026-08-11T13:15:00-04:00", "--before", "1s", "--after", "1s"}, &stdout, &stderr)
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Timeline sample: 1 event(s)") || !strings.Contains(stdout.String(), "source=app-01.example.test") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunReportInfersDatabaseFromServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clogs.yml"), []byte(strings.TrimSpace(`
defaults:
  timezone: America/Chicago
remote:
  defaults:
    out: ./downloads
  servers:
    app-01.example.test:
      timezone: America/New_York
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collection := filepath.Join(root, "downloads", "app-01.example.test", "2026-08-11T120000Z")
	if err := os.MkdirAll(collection, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection, "application.log"), []byte("Aug 11, 2026 1:15:00 PM example.Logger work\nINFO: complete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var ingestOut, ingestErr bytes.Buffer
	if code := Run([]string{"ingest", "--server", "app-01.example.test"}, &ingestOut, &ingestErr); code != ExitSuccess || ingestErr.Len() != 0 {
		t.Fatalf("ingest code=%d stdout=%q stderr=%q", code, ingestOut.String(), ingestErr.String())
	}

	output := filepath.Join(root, "incident.html")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"report", "incident", "--server", "app-01.example.test", "--around", "2026-08-11T13:15:00-04:00", "--output", output}, &stdout, &stderr)
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("missing report output: %v", err)
	}
}

func TestRunReportInfersOutputFromServerAndReportsRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clogs.yml"), []byte(strings.TrimSpace(`
defaults:
  timezone: America/Chicago
paths:
  downloads_root: ./downloads
  db_root: ./data/db
  reports_root: ./reports
remote:
  servers:
    app-01.example.test:
      timezone: America/New_York
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collection := filepath.Join(root, "downloads", "app-01.example.test", "2026-08-11T120000Z")
	if err := os.MkdirAll(collection, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collection, "application.log"), []byte("Aug 11, 2026 1:15:00 PM example.Logger work\nINFO: complete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var ingestOut, ingestErr bytes.Buffer
	if code := Run([]string{"ingest", "--server", "app-01.example.test"}, &ingestOut, &ingestErr); code != ExitSuccess || ingestErr.Len() != 0 {
		t.Fatalf("ingest code=%d stdout=%q stderr=%q", code, ingestOut.String(), ingestErr.String())
	}

	around := "2026-08-11T13:15:00-04:00"
	var stdout, stderr bytes.Buffer
	code := Run([]string{"report", "incident", "--server", "app-01.example.test", "--around", around}, &stdout, &stderr)
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	parsed, err := time.Parse(time.RFC3339Nano, around)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "reports", "incident", "app-01.example.test", parsed.Format("2006-01-02"), parsed.UTC().Format("20060102T150405Z")+"-incident.html")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected report %q: %v", want, err)
	}
}
