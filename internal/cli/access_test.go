package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunParseAccessWithoutTimezone(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "access.log")
	if err := os.WriteFile(path, []byte("10.0.0.1 [05/Aug/2026:07:03:31 -0500] port:1 \"GET /ok HTTP/1.1\" 200 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runParse([]string{path}, &stdout, &stderr); code != ExitSuccess || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"family":"access"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunParseHelpDescribesAccessTimezoneBehavior(t *testing.T) {
	t.Parallel()
	stdout, stderr, code := runForTest([]string{"help", "parse"})
	if code != ExitSuccess || stderr != "" || !strings.Contains(stdout, "access logs contain an explicit numeric offset") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
