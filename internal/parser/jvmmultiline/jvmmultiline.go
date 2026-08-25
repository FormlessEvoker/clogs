// Package jvmmultiline detects and streams JVM Java log events.
package jvmmultiline

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
	headerRE    = regexp.MustCompile(`^([A-Z][a-z]{2}\s+\d{1,2},\s+\d{4}\s+\d{1,2}:\d{2}:\d{2}\s+[AP]M)\s+(.+)$`)
	levelRE     = regexp.MustCompile(`^([A-Z]+):\s?(.*)$`)
	protocolRE  = regexp.MustCompile(`(?i)protocol type:\s*([^\s]+)`)
	exceptionRE = regexp.MustCompile(`(?:Caused by:\s*)?([A-Za-z_$][\w.$]*(?:Exception|Error))(?::\s*(.*))?`)
)

type Options struct {
	Timezone   *time.Location
	SourcePath string
}

type Result struct{ OrphanLines int64 }

// Detect returns a confidence score based on a recognizable JVM header.
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
		return Result{}, fmt.Errorf("JVM parsing requires an IANA timezone")
	}
	buffered := bufio.NewReader(reader)
	var result Result
	var current *assembled
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
		clean := strings.TrimSuffix(line, "\n")
		clean = strings.TrimSuffix(clean, "\r")
		if matches := headerRE.FindStringSubmatch(clean); matches != nil {
			if current != nil {
				if err := current.flush(options, lineNumber-1, emit); err != nil {
					return result, err
				}
			}
			current = &assembled{timestamp: matches[1], context: matches[2], start: lineNumber}
			current.add(line)
		} else if current == nil {
			result.OrphanLines++
		} else {
			current.add(line)
		}
		if eof {
			break
		}
	}
	if current != nil {
		if err := current.flush(options, lineNumber, emit); err != nil {
			return result, err
		}
	}
	return result, nil
}

type assembled struct {
	timestamp, context string
	start              int64
	lines              []string
}

func (event *assembled) add(line string) { event.lines = append(event.lines, line) }

func (event *assembled) flush(options Options, end int64, emit func(model.Event) error) error {
	parsed, err := time.ParseInLocation("Jan 2, 2006 3:04:05 PM", event.timestamp, options.Timezone)
	if err != nil {
		return fmt.Errorf("parse JVM timestamp at line %d: %w", event.start, err)
	}
	logger, operation := splitContext(event.context)
	severity, message, body := "", "", make([]string, 0, len(event.lines)-1)
	for index, raw := range event.lines[1:] {
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		if index == 0 {
			if matches := levelRE.FindStringSubmatch(line); matches != nil {
				severity, message = matches[1], matches[2]
				continue
			}
		}
		body = append(body, line)
	}
	warnings := []string(nil)
	if severity == "" {
		warnings = append(warnings, "missing recognized LEVEL: message line")
	}
	if len(body) > 0 {
		if message != "" {
			message += "\n"
		}
		message += strings.Join(body, "\n")
	}
	java := &model.JavaDetails{Logger: logger, Operation: operation}
	allText := message
	if match := protocolRE.FindStringSubmatch(allText); match != nil {
		java.ProtocolType = match[1]
	}
	for _, line := range append([]string{message}, body...) {
		if match := exceptionRE.FindStringSubmatch(line); match != nil {
			if strings.HasPrefix(strings.TrimSpace(line), "Caused by:") {
				java.RootCauseClass, java.RootCauseMessage = match[1], match[2]
			} else if java.ExceptionClass == "" {
				java.ExceptionClass = match[1]
			}
		}
	}
	if len(body) > 0 {
		java.StackTrace = strings.Join(body, "\n")
	}
	eventModel := model.Event{SourcePath: options.SourcePath, Family: model.FamilyJVMMultiline, OccurredAt: parsed.UTC(), OriginalTimestamp: event.timestamp, Precision: model.PrecisionSecond, SourceLineStart: event.start, SourceLineEnd: end, Severity: severity, Message: message, RawText: strings.Join(event.lines, ""), ParseWarnings: warnings, Java: java}
	eventModel.Signature = signature(eventModel)
	return emit(eventModel)
}

func splitContext(value string) (string, string) {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return strings.Join(parts[:len(parts)-1], " "), parts[len(parts)-1]
}
func signature(event model.Event) string {
	value := strings.Join([]string{string(event.Family), event.Severity, event.Java.Logger, event.Java.Operation, normalize(event.Message)}, "\x00")
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
