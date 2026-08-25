package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunParseNDJSON(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "application.log")
	if err := os.WriteFile(file, []byte("Aug 05, 2026 6:27:33 AM example.Logger work\nINFO: complete\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runParse([]string{file, "--timezone", "America/Chicago"}, &stdout, &stderr)
	if code != ExitSuccess {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"family":"jvm-multiline"`) || !strings.Contains(stdout.String(), `"occurred_at":"2026-08-05T11:27:33Z"`) {
		t.Errorf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunParseRequiresTimezone(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "application.log")
	if err := os.WriteFile(file, []byte("Aug 05, 2026 6:27:33 AM example.Logger work\nINFO: complete\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runParse([]string{file, "--timezone", ""}, &stdout, &stderr)
	if code != ExitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "requires --timezone") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
