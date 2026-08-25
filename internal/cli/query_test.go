package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FormlessEvoker/clogs/internal/ingest"
	"github.com/FormlessEvoker/clogs/internal/storage"
)

func TestRunQueryHumanAndJSON(t *testing.T) {
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
	args := []string{"query", "--db", dbPath, "--around", "2026-08-05T07:03:31-05:00", "--before", "1s", "--after", "1s", "--status", "5xx"}
	stdout, stderr, code := runForTest(args)
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, "Timeline sample: 1 event") || !strings.Contains(stdout, "analysis baseline: 1 event") || !strings.Contains(stdout, "failures=1/1") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	args = append(args, "--format", "json")
	stdout, stderr, code = runForTest(args)
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, `"sample_size":1`) || !strings.Contains(stdout, `"status":"5xx"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunQueryHelpAndValidation(t *testing.T) {
	stdout, stderr, code := runForTest([]string{"help", "query"})
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, "--correlation-window") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runForTest([]string{"query", "--db", "x.db", "--around", "bad"})
	if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "invalid --around") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
