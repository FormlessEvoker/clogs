package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunParseCatalinaRequiresTimezoneAndEmitsNDJSON(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "catalina.log")
	if err := os.WriteFile(path, []byte("05-Aug-2026 07:03:31.216 SEVERE [worker] example.Logger.work test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runParse([]string{path, "--timezone", ""}, &stdout, &stderr); code != ExitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "requires --timezone for Catalina") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runParse([]string{path, "--timezone", "America/Chicago"}, &stdout, &stderr); code != ExitSuccess || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"family":"catalina"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
