package slack

import (
	"testing"
	"time"
)

// A fixed Thursday, 12 June 2026 10:30 local.
var testNow = time.Date(2026, 6, 12, 10, 30, 0, 0, time.UTC)

func TestResolveSchedulePostAt(t *testing.T) {
	cases := []struct {
		name       string
		schedule   string
		scheduleIn string
		want       int64
	}{
		{"none", "", "", 0},
		{"unix passthrough", "1781400000", "", 1781400000}, // ~1.6 days after testNow
		{"iso with timezone", "2026-06-12T18:00:00Z", "", time.Date(2026, 6, 12, 18, 0, 0, 0, time.UTC).Unix()},
		{"iso with offset", "2026-06-12T18:00:00+02:00", "", time.Date(2026, 6, 12, 16, 0, 0, 0, time.UTC).Unix()},
		{"relative minutes", "", "30m", testNow.Unix() + 1800},
		{"relative hours", "", "3h", testNow.Unix() + 3*3600},
		{"relative days", "", "2d", testNow.Unix() + 2*86400},
		{"tomorrow default 9am", "", "tomorrow", time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC).Unix()},
		{"tomorrow 5pm", "", "tomorrow 5pm", time.Date(2026, 6, 13, 17, 0, 0, 0, time.UTC).Unix()},
		{"today later", "", "today 5pm", time.Date(2026, 6, 12, 17, 0, 0, 0, time.UTC).Unix()},
		{"monday 9am", "", "monday 9am", time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC).Unix()},
		{"next friday noon", "", "next friday noon", time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC).Unix()},
		{"friday on a friday-adjacent day", "", "friday", time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC).Add(7 * 24 * time.Hour).Unix()},
	}
	for _, tc := range cases {
		got, err := ResolveSchedulePostAt(tc.schedule, tc.scheduleIn, testNow)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %d (%s), want %d (%s)", tc.name, got, time.Unix(got, 0).UTC(), tc.want, time.Unix(tc.want, 0).UTC())
		}
	}
}

func TestResolveSchedulePostAtErrors(t *testing.T) {
	cases := []struct {
		name       string
		schedule   string
		scheduleIn string
	}{
		{"both set", "2000000000", "3h"},
		{"iso without timezone", "2026-06-12T18:00:00", ""},
		{"prose", "in a bit", ""},
		{"past", "1000000001", ""},
		{"too far out", "", "200d"},
		{"today already passed", "", "today 9am"},
	}
	for _, tc := range cases {
		if _, err := ResolveSchedulePostAt(tc.schedule, tc.scheduleIn, testNow); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestParseReminderDuration(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"30m", testNow.Unix() + 1800},
		{"1.5h", testNow.Unix() + 5400},
		{"2d", testNow.Unix() + 2*86400},
		{"tomorrow", time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC).Unix()},
		{"monday", time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC).Unix()},
		{"2000000000", 2000000000},
		// Widened grammar (shared with --schedule-in; diverges from TS on purpose):
		{"tomorrow 5pm", time.Date(2026, 6, 13, 17, 0, 0, 0, time.UTC).Unix()},
		{"next friday noon", time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC).Unix()},
		{"mon", time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC).Unix()},
	}
	for _, tc := range cases {
		got, err := ParseReminderDuration(tc.input, testNow)
		if err != nil {
			t.Errorf("%q: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %d, want %d", tc.input, got, tc.want)
		}
	}
	if _, err := ParseReminderDuration("whenever", testNow); err == nil {
		t.Error("expected error for prose duration")
	}
}

func TestParseLaterState(t *testing.T) {
	for input, want := range map[string]string{
		"": "in_progress", "active": "in_progress", "in-progress": "in_progress",
		"archive": "archived", "done": "completed", "all": "all",
	} {
		got, err := ParseLaterState(input)
		if err != nil || got != want {
			t.Errorf("ParseLaterState(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseLaterState("bogus"); err == nil {
		t.Error("expected error")
	}
}
