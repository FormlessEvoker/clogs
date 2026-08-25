package timewindow

import (
	"strings"
	"testing"
	"time"
)

func TestResolveErgonomicRanges(t *testing.T) {
	chicago, _ := time.LoadLocation("America/Chicago")
	now := time.Date(2026, 8, 7, 1, 15, 0, 0, chicago)
	tests := []struct {
		name          string
		options       Options
		after, before string
	}{
		{"time only", Options{After: "21:30", Before: "23:15"}, "2026-08-08T02:30:00Z", "2026-08-08T04:15:00Z"},
		{"date only", Options{After: "2026-08-06", Before: "2026-08-07"}, "2026-08-06T05:00:00Z", "2026-08-07T05:00:00Z"},
		{"date and time", Options{After: "2026-08-06 21:30", Before: "2026-08-06T23:15:30"}, "2026-08-07T02:30:00Z", "2026-08-07T04:15:30Z"},
		{"RFC3339", Options{After: "2026-08-06T21:30:00-04:00", Before: "2026-08-06T23:15:00-04:00"}, "2026-08-07T01:30:00Z", "2026-08-07T03:15:00Z"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.options.Now, test.options.Local = now, chicago
			window, err := Resolve(test.options)
			if err != nil {
				t.Fatal(err)
			}
			if window.AfterUTC.Format(time.RFC3339) != test.after || window.BeforeUTC.Format(time.RFC3339) != test.before {
				t.Fatalf("window=%#v", window)
			}
		})
	}
}

func TestResolveSinceAndCalendarDay(t *testing.T) {
	now := time.Date(2026, 8, 7, 4, 0, 0, 0, time.UTC)
	window, err := Resolve(Options{Since: "6h", Now: now})
	if err != nil || window.AfterUTC.Format(time.RFC3339) != "2026-08-06T22:00:00Z" || window.BeforeUTC.Format(time.RFC3339) != "2026-08-07T04:00:00Z" {
		t.Fatalf("window=%#v err=%v", window, err)
	}
	window, err = Resolve(Options{On: "2026-03-08", Timezone: "America/New_York", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if got := window.BeforeUTC.Sub(*window.AfterUTC) + time.Nanosecond; got != 23*time.Hour {
		t.Fatalf("DST calendar day duration=%s", got)
	}
}

func TestResolveRejectsConflictsInvalidAndDSTTimes(t *testing.T) {
	for _, options := range []Options{
		{Since: "6h", On: "2026-08-06"},
		{Since: "0h"},
		{After: "23:00", Before: "22:00", Now: time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC), Local: time.UTC},
		{After: "2026-03-08 02:30", Timezone: "America/New_York"},
		{After: "2026-11-01 01:30", Timezone: "America/New_York"},
	} {
		if _, err := Resolve(options); err == nil {
			t.Fatalf("Resolve(%#v) error=nil", options)
		}
	}
	if _, err := Resolve(Options{After: "nonsense"}); err == nil || !strings.Contains(err.Error(), "HH:mm") {
		t.Fatalf("error=%v", err)
	}
}

func TestContainsUsesInclusiveBoundaries(t *testing.T) {
	after := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	before := after.Add(time.Hour)
	window := &Window{AfterUTC: &after, BeforeUTC: &before}
	if !window.Contains(after) || !window.Contains(before) || window.Contains(after.Add(-time.Nanosecond)) || window.Contains(before.Add(time.Nanosecond)) {
		t.Fatal("inclusive boundary behavior is incorrect")
	}
}

func TestOverlapsImpreciseRemoteTimestamp(t *testing.T) {
	after := time.Date(2026, 8, 7, 12, 0, 30, 0, time.UTC)
	before := after.Add(time.Minute)
	window := &Window{AfterUTC: &after, BeforeUTC: &before}
	minute := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if !window.Overlaps(minute, minute.Add(time.Minute-time.Nanosecond)) {
		t.Fatal("minute-precision interval should overlap the requested window")
	}
}
