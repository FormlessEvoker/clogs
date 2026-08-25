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

func TestRunIncidentReport(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	if err := os.WriteFile(logPath, []byte("10.0.0.1 [06/Aug/2026:21:32:28 -0400] port:1 \"GET /svc/v4/api/site/site-a/netconfig/NetConfig?q=prior HTTP/1.1\" 200 180\n10.0.0.1 [06/Aug/2026:21:32:29 -0400] port:1 \"GET /svc/v4/api/site/site-a/netconfig/NetConfig?q=prior HTTP/1.1\" 200 190\n10.0.0.1 [06/Aug/2026:21:32:31 -0400] port:1 \"GET /svc/v4/api/site/site-a/netconfig/NetConfig?q=burst HTTP/1.1\" 200 512\n10.0.0.1 [06/Aug/2026:21:32:31 -0400] port:1 \"GET /svc/v4/api/site/site-a/netconfig/NetConfig?q=burst HTTP/1.1\" 200 768\n10.0.0.1 [06/Aug/2026:21:32:32 -0400] port:1 \"GET /svc/v4/api/site/site-a/netconfig/NetConfig?q=one HTTP/1.1\" 500 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	catalinaPath := filepath.Join(dir, "catalina.log")
	if err := os.WriteFile(catalinaPath, []byte("06-Aug-2026 21:32:32.000 SEVERE [worker] example.Logger.work failed\n\tjava.lang.OutOfMemoryError: heap\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "clogs.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(context.Background(), db, ingest.Options{Input: dir, Database: dbPath, Source: "server-a", StoreRaw: true, Timezone: "America/Chicago"}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	outputPath := filepath.Join(dir, "incident.html")
	stdout, stderr, code := runForTest([]string{"report", "incident", "--db", dbPath, "--around", "2026-08-06T21:32:32-04:00", "--before", "1m", "--after", "1m", "--timezone", "America/New_York", "--source", "server-a", "--output", outputPath})
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, "Events analyzed:") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "Unified incident timeline") || !strings.Contains(string(contents), "Pre-OOM route pressure") || !strings.Contains(string(contents), "Repeated exact calls") || !strings.Contains(string(contents), "site-a") {
		t.Fatalf("unexpected report: %s", contents)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions=%o", info.Mode().Perm())
	}
}

func TestRunIncidentReportHelpAndValidation(t *testing.T) {
	stdout, stderr, code := runForTest([]string{"help", "report"})
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, "clogs report incident") || !strings.Contains(stdout, "--correlation-window") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runForTest([]string{"report", "incident", "--db", "x.db"})
	if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "requires --db, --around, and --timezone") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	stdout, stderr, code = runForTest([]string{"report", "incident", "--db", "x.db", "--around", "2026-08-06T21:00:00-04:00", "--timezone", "Mars/Olympus", "--output", "x.html"})
	if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "invalid --timezone") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
