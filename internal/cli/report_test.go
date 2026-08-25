package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FormlessEvoker/clogs/internal/ingest"
	"github.com/FormlessEvoker/clogs/internal/storage"
)

func TestRunExportAndStats(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "access.log")
	if err := os.WriteFile(log, []byte("10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /route HTTP/1.1\" 500 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(context.Background(), db, ingest.Options{Input: log, Database: dbPath, Source: "server-a", StoreRaw: true}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	stdout, stderr, code := runForTest([]string{"export", "--db", dbPath, "--source", "server-a"})
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, `"source_label":"server-a"`) || strings.Contains(stdout, "raw_text") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runForTest([]string{"stats", "--db", dbPath, "--json", "--source", "server-a"})
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, `"families"`) || !strings.Contains(stdout, `"access"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunExportHelpAndInvalidUsageStreams(t *testing.T) {
	stdout, stderr, code := runForTest([]string{"help", "export"})
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, "--include-raw") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var out, errOut bytes.Buffer
	code = Run([]string{"stats", "--source", "server-a"}, &out, &errOut)
	if code != ExitUsage || out.Len() != 0 || !strings.Contains(errOut.String(), "requires --db") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}
