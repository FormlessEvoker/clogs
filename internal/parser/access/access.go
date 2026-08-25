// Package access detects and streams custom HTTP access-log events.
package access

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/FormlessEvoker/clogs/internal/model"
)

const MaxLineBytes = 1 << 20

var lineRE = regexp.MustCompile(`^(\S+) \[([^]]+)\] port:(\d+) "([^"\r\n]*)" (\d{3}) (\S+)\s*$`)

// Options configures access-log parsing. RouteTemplates declares route shapes
// used to collapse variable path segments, such as
// "/svc/v4/api/site/{site}". Templates are matched in order against a prefix of
// the request path; when none match, the path is used verbatim as its own
// route template.
type Options struct {
	SourcePath     string
	RouteTemplates []string
}
type LineError struct {
	Line  int64
	Error string
}
type Result struct{ Malformed []LineError }

func Detect(prefix []byte) int {
	for _, line := range strings.Split(string(prefix), "\n") {
		if lineRE.MatchString(strings.TrimSuffix(line, "\r")) {
			return 100
		}
	}
	return 0
}

func Parse(ctx context.Context, reader io.Reader, options Options, emit func(model.Event) error) (Result, error) {
	buffered := bufio.NewReader(reader)
	var result Result
	var lineNumber int64
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		line, eof, err := readLine(buffered)
		if err != nil {
			return result, fmt.Errorf("read line %d: %w", lineNumber+1, err)
		}
		if eof && line == "" {
			break
		}
		lineNumber++
		clean := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.TrimSpace(clean) != "" {
			event, err := parseLine(clean, options.SourcePath, lineNumber, options.RouteTemplates)
			if err != nil {
				result.Malformed = append(result.Malformed, LineError{Line: lineNumber, Error: err.Error()})
			} else if err := emit(event); err != nil {
				return result, err
			}
		}
		if eof {
			break
		}
	}
	return result, nil
}

func parseLine(line, sourcePath string, number int64, routeTemplates []string) (model.Event, error) {
	match := lineRE.FindStringSubmatch(line)
	if match == nil {
		return model.Event{}, fmt.Errorf("does not match access-log format")
	}
	when, err := time.Parse("02/Jan/2006:15:04:05 -0700", match[2])
	if err != nil {
		return model.Event{}, fmt.Errorf("invalid timestamp: %w", err)
	}
	port, err := strconv.Atoi(match[3])
	if err != nil {
		return model.Event{}, fmt.Errorf("invalid port: %w", err)
	}
	request := strings.Fields(match[4])
	if len(request) != 3 {
		return model.Event{}, fmt.Errorf("invalid quoted request")
	}
	status, err := strconv.Atoi(match[5])
	if err != nil {
		return model.Event{}, fmt.Errorf("invalid status: %w", err)
	}
	details := &model.HTTPDetails{ClientAddress: match[1], ServerPort: port, Method: request[0], RawTarget: request[1], HTTPVersion: request[2], Status: status}
	if match[6] != "-" {
		bytes, err := strconv.ParseInt(match[6], 10, 64)
		if err != nil || bytes < 0 {
			return model.Event{}, fmt.Errorf("invalid response bytes")
		}
		details.ResponseBytes = &bytes
	}
	if parsed, err := url.ParseRequestURI(request[1]); err == nil {
		details.Path, details.RawQuery = parsed.Path, parsed.RawQuery
		details.RouteTemplate, details.Site = normalizeRoute(parsed.Path, routeTemplates)
	}
	event := model.Event{SourcePath: sourcePath, Family: model.FamilyAccess, OccurredAt: when.UTC(), OriginalTimestamp: match[2], Precision: model.PrecisionSecond, SourceLineStart: number, SourceLineEnd: number, Message: match[4], RawText: line + "\n", HTTP: details}
	event.Signature = signature(event)
	return event, nil
}

// normalizeRoute rewrites path into a route template using the first matching
// entry in templates, returning the template and the value captured by a
// "{site}" placeholder when one is present. Literal template segments must
// match exactly; a "{name}" segment matches any single non-empty segment. A
// template matches a prefix, so trailing path segments are preserved.
func normalizeRoute(path string, templates []string) (string, string) {
	segments := strings.Split(path, "/")
	for _, template := range templates {
		want := strings.Split(template, "/")
		if len(segments) < len(want) {
			continue
		}
		normalized := append([]string(nil), segments...)
		site := ""
		matched := true
		for index, segment := range want {
			switch {
			case strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}"):
				if segments[index] == "" {
					matched = false
					break
				}
				if segment == "{site}" {
					site = segments[index]
				}
				normalized[index] = segment
			case segment != segments[index]:
				matched = false
			}
			if !matched {
				break
			}
		}
		if matched {
			return strings.Join(normalized, "/"), site
		}
	}
	return path, ""
}
func signature(event model.Event) string {
	value := strings.Join([]string{string(event.Family), event.HTTP.Method, event.HTTP.RouteTemplate, strconv.Itoa(event.HTTP.Status)}, "\x00")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}
func readLine(reader *bufio.Reader) (string, bool, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > MaxLineBytes {
			return "", false, fmt.Errorf("line exceeds %d-byte limit", MaxLineBytes)
		}
		line = append(line, fragment...)
		if err == nil {
			return string(line), false, nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF {
			return string(line), true, nil
		}
		return "", false, err
	}
}
