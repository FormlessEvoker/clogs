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

type listRunner struct{}

func (listRunner) Run(_ context.Context, command download.Command) (string, string, error) {
	if strings.HasPrefix(command.Stdin, "ls") {
		return "-rw-r--r-- 1 u g 1 Aug 5 12:00 application.log.0\n", "", nil
	}
	return "", "", nil
}

type fetchRunner struct{}

func (fetchRunner) Run(_ context.Context, command download.Command) (string, string, error) {
	if strings.HasPrefix(command.Stdin, "ls") {
		return "-rw-r--r-- 1 u g 1 Aug 5 12:00 application.log.0\n", "", nil
	}
	if strings.HasPrefix(command.Stdin, "get ") {
		fields := strings.Split(command.Stdin, " ")
		local := strings.Trim(strings.TrimSpace(fields[len(fields)-1]), "\"")
		return "", "", os.WriteFile(local, []byte("log\n"), 0600)
	}
	return "", "", nil
}

type partialFetchRunner struct{}

func (partialFetchRunner) Run(_ context.Context, command download.Command) (string, string, error) {
	if strings.HasPrefix(command.Stdin, "ls") {
		return "-rw-r--r-- 1 u g 1 Aug 5 12:00 application.log.0\n-rw-r--r-- 1 u g 1 Aug 5 12:00 catalina.2026-08-05.log\n", "", nil
	}
	if strings.Contains(command.Stdin, "catalina") {
		return "", "transfer refused", os.ErrPermission
	}
	if strings.HasPrefix(command.Stdin, "get ") {
		fields := strings.Split(command.Stdin, " ")
		local := strings.Trim(strings.TrimSpace(fields[len(fields)-1]), "\"")
		return "", "", os.WriteFile(local, []byte("log\n"), 0600)
	}
	return "", "", nil
}

func TestRemoteListWritesFilesToStdout(t *testing.T) {
	withNoConfigCWD(t)
	var stdout, stderr bytes.Buffer
	code := runRemote([]string{"list", "prod", "--dir", "/logs"}, &stdout, &stderr, download.Service{Runner: listRunner{}})
	if code != ExitSuccess {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), "application.log.0\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRemoteListUsageErrorWritesStderr(t *testing.T) {
	withNoConfigCWD(t)
	var stdout, stderr bytes.Buffer
	code := runRemote([]string{"list", "prod", "--dir", ""}, &stdout, &stderr, download.Service{})
	if code != ExitUsage {
		t.Fatalf("code = %d", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "requires --dir") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRemoteFetchWritesCollectionToStdout(t *testing.T) {
	withNoConfigCWD(t)

	output := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runRemote([]string{"fetch", "prod", "--dir", "/logs", "--out", output}, &stdout, &stderr, download.Service{
		Runner: fetchRunner{},
		Now:    func() time.Time { return time.Date(2026, 8, 5, 15, 30, 0, 0, time.UTC) },
	})
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Downloaded 1 files from prod:/logs") || !strings.Contains(stdout.String(), "Collection:") || !strings.Contains(stdout.String(), "Manifest:") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(output, "prod", "2026-08-05T153000Z", "manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteFetchPartialFailureWritesDetailsToStderr(t *testing.T) {
	withNoConfigCWD(t)

	var stdout, stderr bytes.Buffer
	code := runRemote([]string{"fetch", "prod", "--dir", "/logs", "--out", t.TempDir()}, &stdout, &stderr, download.Service{
		Runner: partialFetchRunner{},
		Now:    func() time.Time { return time.Date(2026, 8, 5, 15, 30, 0, 0, time.UTC) },
	})
	if code != ExitRuntime || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, value := range []string{"downloaded 1 file(s); 1 transfer(s) failed", "Collection:", "Manifest:", "Failed: catalina.2026-08-05.log: transfer refused"} {
		if !strings.Contains(stderr.String(), value) {
			t.Errorf("stderr missing %q: %q", value, stderr.String())
		}
	}
}

func TestRemoteListAcceptsErgonomicTimeWindows(t *testing.T) {
	withNoConfigCWD(t)
	service := download.Service{Runner: listRunner{}, Now: func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }}
	var stdout, stderr bytes.Buffer
	code := runRemote([]string{"list", "prod", "--dir", "/logs", "--since", "10000h"}, &stdout, &stderr, service)
	if code != ExitSuccess || stderr.Len() != 0 || stdout.String() != "application.log.0\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = runRemote([]string{"list", "prod", "--dir", "/logs", "--after", "2026-08-05", "--before", "2026-08-06", "--tz", "UTC"}, &stdout, &stderr, service)
	if code != ExitSuccess || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRemoteTimeWindowValidation(t *testing.T) {
	withNoConfigCWD(t)
	for _, args := range [][]string{
		{"list", "prod", "--dir", "/logs", "--since", "6h", "--on", "2026-08-06"},
		{"list", "prod", "--dir", "/logs", "--after", "23:00", "--before", "22:00", "--tz", "UTC"},
		{"fetch", "prod", "--dir", "/logs", "--on", "bad-date"},
	} {
		var stdout, stderr bytes.Buffer
		code := runRemote(args, &stdout, &stderr, download.Service{Now: func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }})
		if code != ExitUsage || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRemoteHelpDocumentsTimeWindows(t *testing.T) {
	withNoConfigCWD(t)
	stdout, stderr, code := runForTest([]string{"help", "remote"})
	if code != ExitSuccess || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, value := range []string{"--since 6h", "--after 21:30 --before 23:15", "--on 2026-08-06", "--timezone", "--tz"} {
		if !strings.Contains(stdout, value) {
			t.Errorf("help missing %q: %s", value, stdout)
		}
	}
}

func withNoConfigCWD(t *testing.T) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}
