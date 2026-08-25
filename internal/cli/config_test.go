package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FormlessEvoker/clogs/internal/download"
)

type configRunner struct {
	t       *testing.T
	records []download.Command
}

func (r *configRunner) Run(_ context.Context, command download.Command) (string, string, error) {
	r.records = append(r.records, command)
	if command.Program == "ssh" {
		return "", "", nil
	}
	if strings.HasPrefix(command.Stdin, "ls -l ") {
		return "-rw-r--r-- 1 u g 1 Aug 5 12:00 application.log.0\n", "", nil
	}
	if strings.HasPrefix(command.Stdin, "get ") {
		fields := strings.Fields(command.Stdin)
		if len(fields) < 3 {
			r.t.Fatalf("unexpected get stdin: %q", command.Stdin)
		}
		part := strings.Trim(fields[2], "\"")
		if err := os.WriteFile(part, []byte("payload"), 0o600); err != nil {
			r.t.Fatal(err)
		}
		return "", "", nil
	}
	return "", "", nil
}

func TestRunParseUsesTimezoneFromConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "clogs.yml")
	if err := os.WriteFile(configPath, []byte("parse:\n  timezone: America/Chicago\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "application.log")
	if err := os.WriteFile(file, []byte("Aug 05, 2026 6:27:33 AM example.Logger work\nINFO: complete\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var stdout, stderr bytes.Buffer
	code := Run([]string{"parse", file}, &stdout, &stderr)
	if code != ExitSuccess || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"family":"jvm-multiline"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunFetchUsesNearestConfigAndCLIOverrides(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "clogs.yml"), []byte(strings.TrimSpace(`
remote:
  defaults:
    dir: /parent/logs
    out: `+filepath.Join(parent, "downloads")+`
    timezone: UTC
    since: 12h
  servers:
    server.name:
      dir: /parent/server-logs
      out: `+filepath.Join(parent, "server-downloads")+`
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "clogs.yml"), []byte(strings.TrimSpace(`
remote:
  defaults:
    dir: /child/logs
    out: `+filepath.Join(child, "downloads")+`
    timezone: America/Chicago
    since: 6h
  servers:
    server.name:
      dir: /child/server-logs
      out: `+filepath.Join(child, "server-downloads")+`
`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	fixed := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	runner := &configRunner{t: t}
	var stdout, stderr bytes.Buffer
	code := runRemote([]string{"fetch", "server.name"}, &stdout, &stderr, download.Service{Runner: runner, Now: func() time.Time { return fixed }})
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(runner.records) == 0 || !strings.Contains(runner.records[1].Stdin, "/child/server-logs") {
		t.Fatalf("records=%v", runner.records)
	}
	manifest := filepath.Join(child, "server-downloads", "server.name", "2026-08-05T120000Z", "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	runner = &configRunner{t: t}
	overrideOutput := filepath.Join(root, "override-downloads")
	stdout.Reset()
	stderr.Reset()
	code = runRemote([]string{"fetch", "--dir", "/override/logs", "--out", overrideOutput, "server.name"}, &stdout, &stderr, download.Service{Runner: runner, Now: func() time.Time { return fixed }})
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if len(runner.records) == 0 || !strings.Contains(runner.records[1].Stdin, "/override/logs") {
		t.Fatalf("override records=%v", runner.records)
	}
	overrideManifest := filepath.Join(overrideOutput, "server.name", "2026-08-05T120000Z", "manifest.json")
	if _, err := os.Stat(overrideManifest); err != nil {
		t.Fatalf("override manifest missing: %v", err)
	}
}

func TestRunFetchFailsFastOnInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clogs.yml"), []byte("remote:\n  defaults:\n    dir: /logs\n    since: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	var stdout, stderr bytes.Buffer
	code := Run([]string{"fetch", "server.name"}, &stdout, &stderr)
	if code != ExitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "must be a positive duration") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
