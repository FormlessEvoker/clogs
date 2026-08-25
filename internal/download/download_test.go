package download

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FormlessEvoker/clogs/internal/timewindow"
)

type fakeRunner struct {
	commands []Command
	get      func(Command) error
	listing  string
}

func (r *fakeRunner) Run(_ context.Context, command Command) (string, string, error) {
	r.commands = append(r.commands, command)
	if command.Program == "ssh" {
		return "", "", nil
	}
	if strings.HasPrefix(command.Stdin, "ls -l") {
		if r.listing != "" {
			return r.listing, "", nil
		}
		return "-rw-r--r-- 1 user group 4 Aug 5 12:00 application.log.0\ndrwxr-xr-x 1 user group 0 Aug 5 12:00 ignored\n-rw-r--r-- 1 user group 2 Aug 5 12:00 catalina.2026-08-05.log\n", "", nil
	}
	if r.get != nil {
		return "", "", r.get(command)
	}
	return "", "", nil
}

func TestListUsesControlledSFTPAndDefaultPatterns(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	service := Service{Runner: runner}
	files, err := service.List(context.Background(), ListRequest{Destination: "prod", Directory: "/var/log"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(files, ","), "application.log.0,catalina.2026-08-05.log"; got != want {
		t.Fatalf("files = %q, want %q", got, want)
	}
	if got, want := runner.commands[0].Program, "ssh"; got != want {
		t.Errorf("master program = %q, want %q", got, want)
	}
	if got, want := strings.Join(runner.commands[1].Args[:2], " "), "-oBatchMode=yes -oControlMaster=auto"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	if got := runner.commands[1].Stdin; got != "ls -l \"/var/log\"\n" {
		t.Errorf("batch = %q", got)
	}
}

func TestListCustomPatternsReplaceDefaults(t *testing.T) {
	t.Parallel()
	files, err := (Service{Runner: &fakeRunner{}}).List(context.Background(), ListRequest{Destination: "prod", Directory: "/var/log", Patterns: []string{"catalina.*"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(files, ","), "catalina.2026-08-05.log"; got != want {
		t.Fatalf("files = %q, want %q", got, want)
	}
}

func TestFetchWritesManifestAndRemovesFailedParts(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	runner := &fakeRunner{get: func(command Command) error {
		if strings.Contains(command.Stdin, "catalina") {
			return errors.New("transfer failed")
		}
		parts := strings.Split(command.Stdin, " ")
		local := strings.Trim(strings.TrimSpace(parts[len(parts)-1]), "\"")
		return os.WriteFile(local, []byte("log\n"), 0600)
	}}
	fixed := time.Date(2026, 8, 5, 15, 30, 0, 0, time.UTC)
	result, err := (Service{Runner: runner, Now: func() time.Time { return fixed }}).Fetch(context.Background(), FetchRequest{ListRequest: ListRequest{Destination: "prod", Directory: "/var/log"}, OutputDirectory: temp})
	if err == nil {
		t.Fatal("Fetch() error = nil, want partial failure")
	}
	if len(result.Files) != 1 || len(result.Failures) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(result.Collection, "application.log.0")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(result.Collection, "application.log.0.part")); !os.IsNotExist(err) {
		t.Fatalf("completed .part stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(result.Collection, "catalina.2026-08-05.log.part")); !os.IsNotExist(err) {
		t.Fatalf("failed .part stat error = %v, want not exist", err)
	}
	data, err := os.ReadFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Collection, filepath.Join(temp, "prod", "2026-08-05T153000Z"); got != want {
		t.Errorf("collection = %q, want %q", got, want)
	}
	info, err := os.Stat(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("manifest mode = %v, want 0600", info.Mode().Perm())
	}
	if !strings.Contains(string(data), `"sha256"`) || !strings.Contains(string(data), `"failures"`) || !strings.Contains(string(data), `"remote_modified_at_utc"`) || !strings.Contains(string(data), `"remote_modified_precision": "minute"`) {
		t.Fatalf("manifest missing expected data: %s", data)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Source != "prod" || manifest.RemoteDirectory != "/var/log" || !manifest.CollectedAt.Equal(fixed) || strings.Join(manifest.Patterns, ",") != strings.Join(DefaultPatterns, ",") {
		t.Errorf("manifest identity = %#v", manifest)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Name != "application.log.0" || manifest.Files[0].Size != 4 || manifest.Files[0].SHA256 == "" || manifest.Files[0].RemoteModifiedPrecision != "minute" || manifest.Files[0].RemoteModifiedAtUTC == "" {
		t.Errorf("manifest files = %#v", manifest.Files)
	}
	if len(manifest.Failures) != 1 || manifest.Failures[0].Name != "catalina.2026-08-05.log" || manifest.Failures[0].Error == "" {
		t.Errorf("manifest failures = %#v", manifest.Failures)
	}
	if got, want := countProgram(runner.commands, "ssh"), 2; got != want {
		t.Errorf("ssh command count = %d, want %d (one master and one cleanup)", got, want)
	}
	if got, want := countProgram(runner.commands, "sftp"), 3; got != want {
		t.Errorf("sftp command count = %d, want %d (one list and two transfers)", got, want)
	}
	controlPath := commandOption(runner.commands[1], "-oControlPath=")
	for _, command := range runner.commands[2:4] {
		if got := commandOption(command, "-oControlPath="); got != controlPath {
			t.Errorf("transfer control path = %q, want reused path %q", got, controlPath)
		}
	}
	cleanup := runner.commands[len(runner.commands)-1]
	if cleanup.Program != "ssh" || strings.Join(cleanup.Args, " ") != "-S "+strings.TrimPrefix(controlPath, "-oControlPath=")+" -O exit prod" {
		t.Errorf("cleanup command = %#v, want control-master exit", cleanup)
	}
}

func TestListFiltersByInclusiveModificationWindow(t *testing.T) {
	fixed := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	listed, _, err := parseListingTime("Aug", "7", "11:30", fixed, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	after, before := listed.UTC(), listed.Add(10*time.Minute).UTC()
	runner := &fakeRunner{listing: "-rw-r--r-- 1 user group 4 Aug 7 11:30 application.log.0\n-rw-r--r-- 1 user group 4 Aug 7 11:00 application.log.1\n"}
	service := Service{Runner: runner, Now: func() time.Time { return fixed }}
	files, err := service.List(context.Background(), ListRequest{Destination: "prod", Directory: "/var/log", Window: &timewindow.Window{Mode: "range", Timezone: "local", AfterUTC: &after, BeforeUTC: &before}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(files, ","), "application.log.0"; got != want {
		t.Fatalf("files=%q want=%q", got, want)
	}
}

func TestWindowFilteringExpandsListedPrecision(t *testing.T) {
	t.Parallel()

	after := time.Date(2026, 8, 5, 12, 0, 30, 0, time.UTC)
	before := time.Date(2026, 8, 5, 12, 0, 30, 0, time.UTC)
	window := &timewindow.Window{Mode: "range", Timezone: "UTC", AfterUTC: &after, BeforeUTC: &before}
	if !matchesWindow(window, RemoteFile{ModifiedAt: time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC), Precision: "minute"}) {
		t.Error("minute-precision listing should overlap an instant inside that minute")
	}
	day := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	if !matchesWindow(window, RemoteFile{ModifiedAt: day, Precision: "day"}) {
		t.Error("day-precision listing should overlap an instant inside that day")
	}
}

func TestParseListingTracksMinuteAndDayPrecision(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	files, err := parseListing("-rw-r--r-- 1 u g 1 Dec 31 23:59 recent.log\n-rw-r--r-- 1 u g 1 Jan 2 2020 old.log\n", now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "old.log" || files[0].Precision != "day" || files[0].ModifiedAt.Format(time.RFC3339) != "2020-01-02T00:00:00Z" || files[1].Precision != "minute" || files[1].ModifiedAt.Year() != 2025 {
		t.Fatalf("files=%#v", files)
	}
}

func TestFetchManifestRecordsResolvedWindow(t *testing.T) {
	temp := t.TempDir()
	fixed := time.Date(2026, 8, 7, 15, 30, 0, 0, time.UTC)
	listed, _, err := parseListingTime("Aug", "5", "12:00", fixed, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	after, before := listed.Add(-time.Minute).UTC(), listed.Add(time.Minute).UTC()
	runner := &fakeRunner{listing: "-rw-r--r-- 1 user group 4 Aug 5 12:00 application.log.0\n", get: func(command Command) error {
		parts := strings.Split(command.Stdin, " ")
		local := strings.Trim(strings.TrimSpace(parts[len(parts)-1]), "\"")
		return os.WriteFile(local, []byte("log\n"), 0600)
	}}
	window := &timewindow.Window{Mode: "range", Timezone: "local", AfterUTC: &after, BeforeUTC: &before}
	result, err := (Service{Runner: runner, Now: func() time.Time { return fixed }}).Fetch(context.Background(), FetchRequest{ListRequest: ListRequest{Destination: "prod", Directory: "/var/log", Window: window}, OutputDirectory: temp})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"modification_window"`) || !strings.Contains(string(data), `"after_utc"`) || !strings.Contains(string(data), `"timezone": "local"`) || !strings.Contains(string(data), `"before_utc"`) {
		t.Fatalf("manifest missing resolved window: %s", data)
	}
}

func countProgram(commands []Command, program string) int {
	count := 0
	for _, command := range commands {
		if command.Program == program || (program == "sftp" && command.Program == "") {
			count++
		}
	}
	return count
}

func commandOption(command Command, prefix string) string {
	for _, argument := range command.Args {
		if strings.HasPrefix(argument, prefix) {
			return argument
		}
	}
	return ""
}

func TestListRejectsUnsafeInput(t *testing.T) {
	t.Parallel()
	for _, request := range []ListRequest{
		{Destination: "", Directory: "/logs"},
		{Destination: "-option", Directory: "/logs"},
		{Destination: "prod\nother", Directory: "/logs"},
		{Destination: "prod\x00other", Directory: "/logs"},
		{Destination: "prod", Directory: "relative"},
		{Destination: "prod", Directory: "/logs/../etc"},
		{Destination: "prod", Directory: "/logs\nother"},
		{Destination: "prod", Directory: "/logs\x00other"},
		{Destination: "prod", Directory: "/logs", Patterns: []string{""}},
		{Destination: "prod", Directory: "/logs", Patterns: []string{"name\x00.log"}},
		{Destination: "prod", Directory: "/logs", Patterns: []string{"["}},
	} {
		if _, err := (Service{}).List(context.Background(), request); err == nil {
			t.Errorf("List(%#v) error = nil", request)
		}
	}
}

func TestListingRejectsUnsafeFilenamesBeforeTransfer(t *testing.T) {
	t.Parallel()

	for _, name := range []string{".", "..", "../escape.log", "nested/file.log", `nested\file.log`, "bad\x00name.log"} {
		runner := &fakeRunner{listing: "-rw-r--r-- 1 u g 1 Aug 5 12:00 " + name + "\n"}
		_, err := (Service{Runner: runner}).Fetch(context.Background(), FetchRequest{ListRequest: ListRequest{Destination: "prod", Directory: "/logs"}, OutputDirectory: t.TempDir()})
		if err == nil || countProgram(runner.commands, "sftp") != 1 {
			t.Errorf("name %q err=%v commands=%#v, want rejected before get", name, err, runner.commands)
		}
	}
}

func TestListQuotesValidSpecialCharacterDirectory(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	_, err := (Service{Runner: runner}).List(context.Background(), ListRequest{Destination: "prod", Directory: `/log space/"quoted"/back\slash`})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := runner.commands[1].Stdin, `ls -l "/log space/\"quoted\"/back\\slash"`+"\n"; got != want {
		t.Errorf("list batch = %q, want %q", got, want)
	}
}

func TestFetchSanitizesSourceDirectory(t *testing.T) {
	t.Parallel()

	output := t.TempDir()
	fixed := time.Date(2026, 8, 5, 15, 30, 0, 0, time.UTC)
	runner := &fakeRunner{listing: "-rw-r--r-- 1 u g 1 Aug 5 12:00 application.log.0\n", get: func(command Command) error {
		fields := strings.Split(command.Stdin, " ")
		local := strings.Trim(strings.TrimSpace(fields[len(fields)-1]), "\"")
		return os.WriteFile(local, []byte("log\n"), 0600)
	}}
	result, err := (Service{Runner: runner, Now: func() time.Time { return fixed }}).Fetch(context.Background(), FetchRequest{ListRequest: ListRequest{Destination: "user@host:2222", Directory: "/logs"}, OutputDirectory: output})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Collection, filepath.Join(output, "user_host_2222", "2026-08-05T153000Z"); got != want {
		t.Errorf("collection = %q, want %q", got, want)
	}
}
