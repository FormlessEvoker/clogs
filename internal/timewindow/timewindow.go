// Package timewindow resolves ergonomic command-line time windows into
// absolute instants suitable for modification-time filtering.
package timewindow

import (
	"fmt"
	"strings"
	"time"
)

type Options struct {
	After, Before, On, Since, Timezone string
	Now                                time.Time
	Local                              *time.Location
}

type Window struct {
	Mode      string     `json:"mode"`
	Timezone  string     `json:"timezone"`
	AfterUTC  *time.Time `json:"after_utc,omitempty"`
	BeforeUTC *time.Time `json:"before_utc,omitempty"`
}

func Resolve(options Options) (*Window, error) {
	modes := 0
	if options.On != "" {
		modes++
	}
	if options.Since != "" {
		modes++
	}
	if options.After != "" || options.Before != "" {
		modes++
	}
	if modes == 0 {
		return nil, nil
	}
	if modes > 1 {
		return nil, fmt.Errorf("--on, --since, and --after/--before are mutually exclusive")
	}
	local := options.Local
	if local == nil {
		local = time.Local
	}
	location := local
	timezone := "local"
	if options.Timezone != "" {
		var err error
		location, err = time.LoadLocation(options.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %q: %w", options.Timezone, err)
		}
		timezone = options.Timezone
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	window := &Window{Timezone: timezone}
	if options.Since != "" {
		duration, err := time.ParseDuration(options.Since)
		if err != nil || duration <= 0 {
			return nil, fmt.Errorf("--since must be a positive duration such as 6h")
		}
		after, before := now.Add(-duration).UTC(), now.UTC()
		window.Mode, window.AfterUTC, window.BeforeUTC = "since", &after, &before
		return window, nil
	}
	if options.On != "" {
		date, err := time.ParseInLocation("2006-01-02", options.On, location)
		if err != nil {
			return nil, fmt.Errorf("--on must be a date in YYYY-MM-DD form")
		}
		after := date.UTC()
		before := date.AddDate(0, 0, 1).Add(-time.Nanosecond).UTC()
		window.Mode, window.AfterUTC, window.BeforeUTC = "on", &after, &before
		return window, nil
	}
	window.Mode = "range"
	if options.After != "" {
		value, err := parseBoundary(options.After, now, location)
		if err != nil {
			return nil, fmt.Errorf("invalid --after: %w", err)
		}
		value = value.UTC()
		window.AfterUTC = &value
	}
	if options.Before != "" {
		value, err := parseBoundary(options.Before, now, location)
		if err != nil {
			return nil, fmt.Errorf("invalid --before: %w", err)
		}
		value = value.UTC()
		window.BeforeUTC = &value
	}
	if window.AfterUTC != nil && window.BeforeUTC != nil && window.AfterUTC.After(*window.BeforeUTC) {
		return nil, fmt.Errorf("--after must not be later than --before")
	}
	return window, nil
}

func (window *Window) Contains(value time.Time) bool {
	return window.Overlaps(value, value)
}

// Overlaps conservatively matches an imprecise modification-time interval.
// This prevents a minute- or day-precision SFTP listing from excluding a file
// that may fall inside the requested window.
func (window *Window) Overlaps(start, end time.Time) bool {
	if window == nil {
		return true
	}
	start, end = start.UTC(), end.UTC()
	return (window.AfterUTC == nil || !end.Before(*window.AfterUTC)) && (window.BeforeUTC == nil || !start.After(*window.BeforeUTC))
}

func (window *Window) Summary() string {
	if window == nil {
		return "(none)"
	}
	after, before := "unbounded", "unbounded"
	if window.AfterUTC != nil {
		after = window.AfterUTC.Format(time.RFC3339)
	}
	if window.BeforeUTC != nil {
		before = window.BeforeUTC.Format(time.RFC3339)
	}
	return after + " through " + before + " (input timezone: " + window.Timezone + ")"
}

func parseBoundary(input string, now time.Time, location *time.Location) (time.Time, error) {
	if value, err := time.Parse(time.RFC3339Nano, input); err == nil {
		return value, nil
	}
	formats := []struct {
		layout string
		value  string
	}{
		{"2006-01-02 15:04:05", input},
		{"2006-01-02T15:04:05", input},
		{"2006-01-02 15:04", input},
		{"2006-01-02T15:04", input},
		{"2006-01-02", input},
	}
	if strings.Count(input, ":") >= 1 && !strings.ContainsAny(input, "T ") {
		date := now.In(location).Format("2006-01-02")
		formats = append([]struct{ layout, value string }{{"2006-01-02 15:04:05", date + " " + input}, {"2006-01-02 15:04", date + " " + input}}, formats...)
	}
	for _, candidate := range formats {
		value, err := time.ParseInLocation(candidate.layout, candidate.value, location)
		if err != nil {
			continue
		}
		if value.In(location).Format(candidate.layout) != candidate.value {
			return time.Time{}, fmt.Errorf("%q is not a real local time in %s (DST transition)", input, location)
		}
		if strings.Contains(candidate.layout, "15:04") && ambiguous(value, candidate.layout, candidate.value, location) {
			return time.Time{}, fmt.Errorf("%q is ambiguous in %s; include an RFC3339 offset", input, location)
		}
		return value, nil
	}
	return time.Time{}, fmt.Errorf("use HH:mm, YYYY-MM-DD, YYYY-MM-DD HH:mm, or RFC3339")
}

func ambiguous(value time.Time, layout, input string, location *time.Location) bool {
	for _, offset := range []time.Duration{-2 * time.Hour, -time.Hour, time.Hour, 2 * time.Hour} {
		other := value.Add(offset)
		if other.Equal(value) {
			continue
		}
		if other.In(location).Format(layout) == input {
			return true
		}
	}
	return false
}
