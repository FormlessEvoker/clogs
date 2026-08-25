// Package download retrieves allowlisted log files through the local OpenSSH
// SFTP client. It deliberately has no knowledge of parsing or storage.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FormlessEvoker/clogs/internal/timewindow"
)

var DefaultPatterns = []string{"application.log*", "access_log*.log", "catalina.*.log"}

type Command struct {
	Program string
	Args    []string
	Stdin   string
}

type Runner interface {
	Run(context.Context, Command) (stdout, stderr string, err error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, command Command) (string, string, error) {
	program := command.Program
	if program == "" {
		program = "sftp"
	}
	cmd := exec.CommandContext(ctx, program, command.Args...)
	cmd.Stdin = strings.NewReader(command.Stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

type Service struct {
	Runner Runner
	Now    func() time.Time
}

func NewService() Service { return Service{Runner: execRunner{}, Now: time.Now} }

type ListRequest struct {
	Destination string
	Directory   string
	Patterns    []string
	Window      *timewindow.Window
}

type FetchRequest struct {
	ListRequest
	OutputDirectory string
}

type FileFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type FileRecord struct {
	Name                    string `json:"name"`
	Size                    int64  `json:"size"`
	SHA256                  string `json:"sha256"`
	RemoteModifiedAtUTC     string `json:"remote_modified_at_utc,omitempty"`
	RemoteModifiedPrecision string `json:"remote_modified_precision,omitempty"`
}

type Manifest struct {
	Source             string             `json:"source"`
	RemoteDirectory    string             `json:"remote_directory"`
	Patterns           []string           `json:"patterns"`
	CollectedAt        time.Time          `json:"collected_at"`
	ModificationWindow *timewindow.Window `json:"modification_window,omitempty"`
	Files              []FileRecord       `json:"files"`
	Failures           []FileFailure      `json:"failures,omitempty"`
}

type FetchResult struct {
	Collection string
	Manifest   string
	Files      []FileRecord
	Failures   []FileFailure
}

func (s Service) List(ctx context.Context, request ListRequest) ([]string, error) {
	patterns, err := validateRequest(request)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var files []RemoteFile
	err = s.withConnection(ctx, request.Destination, func(options []string) error {
		var listErr error
		files, listErr = s.list(ctx, request, options, now)
		return listErr
	})
	if err != nil {
		return nil, err
	}
	matched := matching(files, patterns, request.Window)
	names := make([]string, len(matched))
	for index, file := range matched {
		names[index] = file.Name
	}
	return names, nil
}

func (s Service) Fetch(ctx context.Context, request FetchRequest) (FetchResult, error) {
	patterns, err := validateRequest(request.ListRequest)
	if err != nil {
		return FetchResult{}, err
	}
	output := request.OutputDirectory
	if output == "" {
		output = "downloads"
	}
	now := s.now()
	var result FetchResult
	err = s.withConnection(ctx, request.Destination, func(options []string) error {
		files, err := s.list(ctx, request.ListRequest, options, now)
		if err != nil {
			return err
		}
		files = matching(files, patterns, request.Window)
		collection := filepath.Join(output, sanitizeSource(request.Destination), now.UTC().Format("2006-01-02T150405Z"))
		if err := os.MkdirAll(filepath.Dir(collection), 0700); err != nil {
			return fmt.Errorf("create collection parent: %w", err)
		}
		if err := os.Mkdir(collection, 0700); err != nil {
			return fmt.Errorf("create collection %q: %w", collection, err)
		}
		result = FetchResult{Collection: collection, Manifest: filepath.Join(collection, "manifest.json")}
		for _, remoteFile := range files {
			name := remoteFile.Name
			part := filepath.Join(collection, name+".part")
			final := filepath.Join(collection, name)
			remote := path.Join(request.Directory, name)
			_, stderr, transferErr := s.runner().Run(ctx, Command{
				Args:  append(append([]string{}, options...), "-b", "-", request.Destination),
				Stdin: "get " + sftpQuote(remote) + " " + sftpQuote(part) + "\n",
			})
			if transferErr != nil {
				_ = os.Remove(part) // Failed transfers are never mistaken for completed logs.
				result.Failures = append(result.Failures, FileFailure{Name: name, Error: conciseError(stderr, transferErr)})
				continue
			}
			if err := os.Rename(part, final); err != nil {
				_ = os.Remove(part)
				result.Failures = append(result.Failures, FileFailure{Name: name, Error: err.Error()})
				continue
			}
			_ = os.Chmod(final, 0600)
			record, err := digest(final, name)
			if err != nil {
				result.Failures = append(result.Failures, FileFailure{Name: name, Error: err.Error()})
				continue
			}
			record.RemoteModifiedAtUTC = remoteFile.ModifiedAt.UTC().Format(time.RFC3339)
			record.RemoteModifiedPrecision = remoteFile.Precision
			result.Files = append(result.Files, record)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	manifest := Manifest{Source: request.Destination, RemoteDirectory: request.Directory, Patterns: patterns, CollectedAt: now.UTC(), ModificationWindow: request.Window, Files: result.Files, Failures: result.Failures}
	if err := writeManifest(result.Manifest, manifest); err != nil {
		return result, err
	}
	if len(result.Failures) > 0 {
		return result, fmt.Errorf("downloaded %d file(s); %d transfer(s) failed", len(result.Files), len(result.Failures))
	}
	return result, nil
}

type RemoteFile struct {
	Name       string
	ModifiedAt time.Time
	Precision  string
}

func (s Service) list(ctx context.Context, request ListRequest, options []string, now time.Time) ([]RemoteFile, error) {
	stdout, stderr, err := s.runner().Run(ctx, Command{
		Args:  append(append([]string{}, options...), "-b", "-", request.Destination),
		Stdin: "ls -l " + sftpQuote(request.Directory) + "\n",
	})
	if err != nil {
		return nil, commandError("list", request.Destination, stderr, err)
	}
	files, err := parseListing(stdout, now, time.Local)
	if err != nil {
		return nil, fmt.Errorf("list %s:%s: %w", request.Destination, request.Directory, err)
	}
	return files, nil
}

func (s Service) withConnection(ctx context.Context, destination string, operation func([]string) error) error {
	directory, err := os.MkdirTemp("", "clogs-sftp-")
	if err != nil {
		return fmt.Errorf("create OpenSSH control directory: %w", err)
	}
	defer os.RemoveAll(directory)
	controlPath := filepath.Join(directory, "control")
	masterArgs := []string{"-MNf", "-oControlMaster=yes", "-oControlPersist=yes", "-oControlPath=" + controlPath, destination}
	_, stderr, err := s.runner().Run(ctx, Command{Program: "ssh", Args: masterArgs})
	if err != nil {
		return commandError("connect", destination, stderr, err)
	}
	defer func() {
		_, _, _ = s.runner().Run(context.Background(), Command{Program: "ssh", Args: []string{"-S", controlPath, "-O", "exit", destination}})
	}()
	return operation([]string{"-oBatchMode=yes", "-oControlMaster=auto", "-oControlPath=" + controlPath})
}

func (s Service) runner() Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return execRunner{}
}
func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func validateRequest(request ListRequest) ([]string, error) {
	if request.Destination == "" || strings.HasPrefix(request.Destination, "-") || hasControl(request.Destination) {
		return nil, errors.New("SSH destination must be a non-empty OpenSSH alias or destination")
	}
	if !strings.HasPrefix(request.Directory, "/") || hasControl(request.Directory) || strings.Contains(request.Directory, "..") {
		return nil, errors.New("remote directory must be an absolute path without '..'")
	}
	patterns := request.Patterns
	if len(patterns) == 0 {
		patterns = DefaultPatterns
	}
	for _, pattern := range patterns {
		if pattern == "" || hasControl(pattern) {
			return nil, fmt.Errorf("invalid filename pattern %q", pattern)
		}
		if _, err := path.Match(pattern, "example.log"); err != nil {
			return nil, fmt.Errorf("invalid filename pattern %q: %w", pattern, err)
		}
	}
	return append([]string(nil), patterns...), nil
}

func parseListing(stdout string, now time.Time, location *time.Location) ([]RemoteFile, error) {
	var files []RemoteFile
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "sftp>") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "-") {
			continue
		} // Ignore non-regular entries and client chatter.
		if len(fields) != 9 {
			return nil, fmt.Errorf("remote listing contains unsupported or unsafe entry %q", line)
		}
		name := fields[len(fields)-1]
		if !safeFilename(name) {
			return nil, fmt.Errorf("remote listing contains unsafe filename %q", name)
		}
		modifiedAt, precision, err := parseListingTime(fields[5], fields[6], fields[7], now, location)
		if err != nil {
			return nil, fmt.Errorf("remote listing contains unsupported modification time in %q: %w", line, err)
		}
		files = append(files, RemoteFile{Name: name, ModifiedAt: modifiedAt, Precision: precision})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

func parseListingTime(month, day, clockOrYear string, now time.Time, location *time.Location) (time.Time, string, error) {
	dayNumber, err := strconv.Atoi(day)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid day %q", day)
	}
	monthTime, err := time.Parse("Jan", month)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid month %q", month)
	}
	if strings.Contains(clockOrYear, ":") {
		parts := strings.Split(clockOrYear, ":")
		if len(parts) != 2 {
			return time.Time{}, "", fmt.Errorf("invalid time %q", clockOrYear)
		}
		hour, hourErr := strconv.Atoi(parts[0])
		minute, minuteErr := strconv.Atoi(parts[1])
		if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return time.Time{}, "", fmt.Errorf("invalid time %q", clockOrYear)
		}
		localNow := now.In(location)
		candidates := []time.Time{
			time.Date(localNow.Year()-1, monthTime.Month(), dayNumber, hour, minute, 0, 0, location),
			time.Date(localNow.Year(), monthTime.Month(), dayNumber, hour, minute, 0, 0, location),
			time.Date(localNow.Year()+1, monthTime.Month(), dayNumber, hour, minute, 0, 0, location),
		}
		best := candidates[0]
		for _, candidate := range candidates[1:] {
			if absTime(candidate.Sub(now)) < absTime(best.Sub(now)) {
				best = candidate
			}
		}
		if best.Month() != monthTime.Month() || best.Day() != dayNumber {
			return time.Time{}, "", fmt.Errorf("invalid calendar date")
		}
		return best, "minute", nil
	}
	year, err := strconv.Atoi(clockOrYear)
	if err != nil || year < 1970 || year > 9999 {
		return time.Time{}, "", fmt.Errorf("invalid year %q", clockOrYear)
	}
	value := time.Date(year, monthTime.Month(), dayNumber, 0, 0, 0, 0, location)
	if value.Month() != monthTime.Month() || value.Day() != dayNumber {
		return time.Time{}, "", fmt.Errorf("invalid calendar date")
	}
	return value, "day", nil
}

func matching(files []RemoteFile, patterns []string, window *timewindow.Window) []RemoteFile {
	matched := make([]RemoteFile, 0, len(files))
	for _, file := range files {
		for _, pattern := range patterns {
			ok, _ := path.Match(pattern, file.Name)
			if ok && matchesWindow(window, file) {
				matched = append(matched, file)
				break
			}
		}
	}
	return matched
}

func matchesWindow(window *timewindow.Window, file RemoteFile) bool {
	end := file.ModifiedAt
	switch file.Precision {
	case "minute":
		end = end.Add(time.Minute - time.Nanosecond)
	case "day":
		end = end.AddDate(0, 0, 1).Add(-time.Nanosecond)
	}
	return window.Overlaps(file.ModifiedAt, end)
}

func absTime(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func safeFilename(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\") && !hasControl(name)
}
func hasControl(value string) bool { return strings.ContainsAny(value, "\r\n\x00") }
func sftpQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}
func sanitizeSource(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-_", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "source"
	}
	return b.String()
}
func commandError(operation, destination, stderr string, err error) error {
	return fmt.Errorf("SFTP %s for %s: %s", operation, destination, conciseError(stderr, err))
}
func conciseError(stderr string, err error) string {
	if text := strings.TrimSpace(stderr); text != "" {
		return text
	}
	return err.Error()
}

func digest(filename, name string) (FileRecord, error) {
	file, err := os.Open(filename)
	if err != nil {
		return FileRecord{}, fmt.Errorf("hash %q: %w", name, err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return FileRecord{}, fmt.Errorf("hash %q: %w", name, err)
	}
	return FileRecord{Name: name, Size: size, SHA256: fmt.Sprintf("%x", hash.Sum(nil))}, nil
}
func writeManifest(filename string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(filename, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}
