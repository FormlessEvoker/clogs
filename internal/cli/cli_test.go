package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRootHelp(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runForTest(nil)
	if code != ExitSuccess {
		t.Fatalf("Run() exit code = %d, want %d", code, ExitSuccess)
	}
	if !strings.Contains(stdout, "Available commands:") {
		t.Errorf("stdout missing available commands: %q", stdout)
	}
	const commands = `  version    print the application version
  fetch      remote fetch logs using config defaults
  list       remote list logs using config defaults
  remote     list and fetch logs over SFTP
  parse      inspect a local log as NDJSON
  ingest     ingest local log files into SQLite
  stats      summarize an ingested database
  export     export events from a SQLite database
  query      query and analyze a merged event timeline
  report     generate a self-contained incident report`
	if !strings.Contains(stdout, commands) {
		t.Errorf("stdout missing current command list: %q", stdout)
	}
	if strings.Contains(stdout, "Planned commands (not yet implemented):\n  remote") {
		t.Errorf("stdout incorrectly lists remote as planned: %q", stdout)
	}
	if !strings.Contains(stdout, "  report     generate a self-contained incident report") || strings.Contains(stdout, "Planned commands") {
		t.Errorf("stdout does not list report as implemented: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestRunRootHelpAliases(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		stdout, stderr, code := runForTest(args)
		if code != ExitSuccess {
			t.Errorf("Run(%v) exit code = %d, want %d", args, code, ExitSuccess)
		}
		if !strings.Contains(stdout, "Available commands:") {
			t.Errorf("Run(%v) stdout missing root help: %q", args, stdout)
		}
		if stderr != "" {
			t.Errorf("Run(%v) stderr = %q, want empty", args, stderr)
		}
	}
}

func TestRunCommandHelp(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		command  string
		usage    string
		directly bool
	}{
		{command: "version", usage: "clogs version", directly: true},
		{command: "remote", usage: "clogs remote list", directly: true},
		{command: "parse", usage: "clogs parse", directly: true},
		{command: "ingest", usage: "clogs ingest", directly: true},
		{command: "stats", usage: "clogs stats", directly: true},
		{command: "export", usage: "clogs export", directly: true},
		{command: "query", usage: "clogs query", directly: true},
		{command: "report", usage: "clogs report incident", directly: true},
	} {
		test := test
		t.Run(test.command, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runForTest([]string{"help", test.command})
			if code != ExitSuccess {
				t.Fatalf("Run(help %s) exit code = %d, want %d", test.command, code, ExitSuccess)
			}
			if !strings.Contains(stdout, test.usage) {
				t.Errorf("Run(help %s) stdout missing %q: %q", test.command, test.usage, stdout)
			}
			if stderr != "" {
				t.Errorf("Run(help %s) stderr = %q, want empty", test.command, stderr)
			}
			if !test.directly {
				return
			}
			stdout, stderr, code = runForTest([]string{test.command, "--help"})
			if code != ExitSuccess || !strings.Contains(stdout, test.usage) || stderr != "" {
				t.Errorf("Run(%s --help) = (stdout %q, stderr %q, code %d), want command help on stdout and code %d", test.command, stdout, stderr, code, ExitSuccess)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runForTest([]string{"version"})
	if code != ExitSuccess {
		t.Fatalf("Run() exit code = %d, want %d", code, ExitSuccess)
	}
	if got, want := stdout, "clogs dev\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestRunVersionHelp(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runForTest([]string{"help", "version"})
	if code != ExitSuccess {
		t.Fatalf("Run() exit code = %d, want %d", code, ExitSuccess)
	}
	if !strings.Contains(stdout, "clogs version") {
		t.Errorf("stdout missing version usage: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestRunUsageErrorsWriteOnlyToStderr(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runForTest([]string{"ingest", "--db", "clogs.db"})
	if code != ExitUsage {
		t.Fatalf("Run() exit code = %d, want %d", code, ExitUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "requires exactly one file") {
		t.Errorf("stderr missing usage error: %q", stderr)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runForTest([]string{"unknown"})
	if code != ExitUsage {
		t.Fatalf("Run() exit code = %d, want %d", code, ExitUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `unknown command "unknown"`) {
		t.Errorf("stderr missing unknown-command error: %q", stderr)
	}
}

func TestRunDispatcherUsageErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		args    []string
		message string
	}{
		{name: "root help arguments", args: []string{"--help", "version"}, message: "--help does not accept additional arguments"},
		{name: "help arguments", args: []string{"help", "version", "extra"}, message: "help accepts at most one command"},
		{name: "unknown help command", args: []string{"help", "unknown"}, message: `unknown command "unknown"`},
		{name: "version arguments", args: []string{"version", "extra"}, message: "version does not accept positional arguments"},
		{name: "unknown remote command", args: []string{"remote", "sync"}, message: `unknown remote command "sync"`},
		{name: "unknown report command", args: []string{"report", "summary"}, message: `report supports only the incident subcommand, not "summary"`},
		{name: "export malformed flag", args: []string{"export", "--db"}, message: "flag needs an argument"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runForTest(test.args)
			if code != ExitUsage {
				t.Fatalf("Run(%v) exit code = %d, want %d", test.args, code, ExitUsage)
			}
			if stdout != "" {
				t.Errorf("Run(%v) stdout = %q, want empty", test.args, stdout)
			}
			if !strings.Contains(stderr, test.message) {
				t.Errorf("Run(%v) stderr missing %q: %q", test.args, test.message, stderr)
			}
		})
	}
}

func runForTest(args []string) (stdout, stderr string, code int) {
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return out.String(), errOut.String(), code
}
