package main

import (
	"bytes"
	"testing"
)

func TestRunDelegatesToCLI(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if got, want := run([]string{"version"}, &stdout, &stderr), 0; got != want {
		t.Fatalf("run() exit code = %d, want %d", got, want)
	}

	if got, want := stdout.String(), "clogs dev\n"; got != want {
		t.Errorf("run() output = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("run() stderr = %q, want empty", got)
	}
}

func TestRunPreservesCLIErrorStreamsAndExitCode(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if got, want := run([]string{"unknown"}, &stdout, &stderr), 2; got != want {
		t.Fatalf("run() exit code = %d, want %d", got, want)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("run() stdout = %q, want empty", got)
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte(`unknown command "unknown"`)) {
		t.Errorf("run() stderr = %q, want unknown-command diagnostic", got)
	}
}
