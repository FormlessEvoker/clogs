package cli

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunIngestCreatesQueryableDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "application.log")
	if err := os.WriteFile(logPath, []byte("Aug 05, 2026 6:27:33 AM example.Logger work\nINFO: complete\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "clogs.db")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"ingest", logPath, "--db", dbPath, "--source", "server-a", "--timezone", "America/Chicago"}, &stdout, &stderr)
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Source:           server-a") || !strings.Contains(stdout.String(), "JVM events:       1") {
		t.Errorf("stdout = %q", stdout.String())
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count); err != nil || count != 1 {
		t.Fatalf("events=%d err=%v", count, err)
	}
}

func TestRunIngestHelpWritesStdout(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runForTest([]string{"help", "ingest"})
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, "--store-raw") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
