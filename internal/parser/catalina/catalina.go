// Package catalina detects and streams Tomcat Catalina log events.
package catalina

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/FormlessEvoker/clogs/internal/model"
)

const MaxLineBytes = 1 << 20

var (
	headerRE    = regexp.MustCompile(`^(\d{2}-[A-Z][a-z]{2}-\d{4}\s+\d{2}:\d{2}:\d{2}\.\d{3})\s+([A-Z]+)\s+\[([^]]*)\]\s+(\S+)\s*(.*)$`)
	exceptionRE = regexp.MustCompile(`(?:Caused by:\s*)?([A-Za-z_$][\w.$]*(?:Exception|Error))(?::\s*(.*))?`)
)

type Options struct {
	Timezone   *time.Location
	SourcePath string
}
type Result struct{ OrphanLines int64 }

func Detect(prefix []byte) int {
	for _, line := range strings.Split(string(prefix), "\n") {
		if headerRE.MatchString(strings.TrimSuffix(line, "\r")) {
			return 100
		}
	}
	return 0
}

func Parse(ctx context.Context, reader io.Reader, options Options, emit func(model.Event) error) (Result, error) {
	if options.Timezone == nil {
		return Result{}, fmt.Errorf("Catalina parsing requires an IANA timezone")
	}
	buffered := bufio.NewReader(reader)
	var result Result
	var current *assembled
	var number int64
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		line, eof, err := readLine(buffered)
		if err != nil {
			return result, fmt.Errorf("read line %d: %w", number+1, err)
		}
		if eof && line == "" {
			break
		}
		number++
		clean := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if match := headerRE.FindStringSubmatch(clean); match != nil {
			if current != nil {
				if err := current.flush(options, number-1, emit); err != nil {
					return result, err
				}
			}
			current = &assembled{header: match, start: number}
			current.lines = append(current.lines, line)
		} else if current == nil {
			result.OrphanLines++
		} else {
			current.lines = append(current.lines, line)
		}
		if eof {
			break
		}
	}
	if current != nil {
		if err := current.flush(options, number, emit); err != nil {
			return result, err
		}
	}
	return result, nil
}

type assembled struct {
	header []string
	start  int64
	lines  []string
}

func (a *assembled) flush(options Options, end int64, emit func(model.Event) error) error {
	when, err := time.ParseInLocation("02-Jan-2006 15:04:05.000", a.header[1], options.Timezone)
	if err != nil {
		return fmt.Errorf("parse Catalina timestamp at line %d: %w", a.start, err)
	}
	logger := a.header[4]
	operation := logger
	if index := strings.LastIndex(logger, "."); index >= 0 {
		operation = logger[index+1:]
	}
	message := a.header[5]
	body := make([]string, 0, len(a.lines)-1)
	for _, line := range a.lines[1:] {
		body = append(body, strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
	}
	java := &model.JavaDetails{Logger: logger, Operation: operation, Thread: a.header[3]}
	for _, line := range append([]string{message}, body...) {
		if match := exceptionRE.FindStringSubmatch(line); match != nil {
			if strings.HasPrefix(strings.TrimSpace(line), "Caused by:") {
				java.RootCauseClass, java.RootCauseMessage = match[1], match[2]
			} else if java.ExceptionClass == "" {
				java.ExceptionClass = match[1]
				java.ExceptionMessage = match[2]
			}
		}
	}
	if len(body) > 0 {
		java.StackTrace = strings.Join(body, "\n")
	}
	event := model.Event{SourcePath: options.SourcePath, Family: model.FamilyCatalina, OccurredAt: when.UTC(), OriginalTimestamp: a.header[1], Precision: model.PrecisionMillisecond, SourceLineStart: a.start, SourceLineEnd: end, Severity: a.header[2], Message: message, RawText: strings.Join(a.lines, ""), Java: java}
	event.Signature = signature(event)
	return emit(event)
}
func signature(event model.Event) string {
	java := event.Java
	value := strings.Join([]string{string(event.Family), event.Severity, java.Logger, normalize(event.Message), java.ExceptionClass, java.RootCauseClass, normalize(java.RootCauseMessage)}, "\x00")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}
func normalize(value string) string { return strings.Join(strings.Fields(value), " ") }
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
