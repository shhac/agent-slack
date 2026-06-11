package slack

import (
	"reflect"
	"testing"

	"github.com/shhac/agent-slack/internal/render"
)

func TestSplitEmailsFromInviteTargets(t *testing.T) {
	emails, nonEmails := SplitEmailsFromInviteTargets([]string{
		"a@b.com",
		" a@b.com ", // same email, whitespace — must not double-invite
		"U12345678",
		"@alice",
		"not-an-email",
		"c@d.org",
	})
	if !reflect.DeepEqual(emails, []string{"a@b.com", "c@d.org"}) {
		t.Errorf("emails = %v", emails)
	}
	if !reflect.DeepEqual(nonEmails, []string{"U12345678", "@alice", "not-an-email"}) {
		t.Errorf("nonEmails = %v", nonEmails)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"diagram.png":     "diagram.png",
		`a/b\c:d"e|f?g*h`: "a_b_c_d_e_f_g_h",
		"..":              "_", // must not resolve to the parent dir
		".":               "_",
		"..hidden":        "..hidden", // only bare dot-names are dangerous
	}
	for input, want := range cases {
		if got := sanitizeFilename(input); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPassesReactionNameFilters(t *testing.T) {
	msg := map[string]any{
		"reactions": []any{
			map[string]any{"name": "eyes"},
			map[string]any{"name": "rocket"},
		},
	}
	cases := []struct {
		name    string
		with    []string
		without []string
		want    bool
	}{
		{"no filters", nil, nil, true},
		{"with present", []string{"eyes"}, nil, true},
		{"with missing", []string{"tada"}, nil, false},
		{"with all present", []string{"eyes", "rocket"}, nil, true},
		{"with one missing", []string{"eyes", "tada"}, nil, false},
		{"without absent", nil, []string{"tada"}, true},
		{"without present", nil, []string{"rocket"}, false},
		{"combined", []string{"eyes"}, []string{"tada"}, true},
	}
	for _, tc := range cases {
		if got := passesReactionNameFilters(msg, tc.with, tc.without); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if !passesReactionNameFilters(map[string]any{}, nil, []string{"x"}) {
		t.Error("message without reactions should pass a without-filter")
	}
	if passesReactionNameFilters(map[string]any{}, []string{"x"}, nil) {
		t.Error("message without reactions should fail a with-filter")
	}
}

func TestDateToUnixSeconds(t *testing.T) {
	start, err := dateToUnixSeconds("2026-06-12", false)
	if err != nil {
		t.Fatal(err)
	}
	end, err := dateToUnixSeconds("2026-06-12", true)
	if err != nil {
		t.Fatal(err)
	}
	if end-start != 86399 { // 23:59:59.999 truncated to seconds
		t.Errorf("end-start = %d, want 86399", end-start)
	}
	for _, bad := range []string{"June 1", "2026-6-1", "2026-06-12T10:00", ""} {
		if _, err := dateToUnixSeconds(bad, false); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestInferFileExt(t *testing.T) {
	cases := []struct {
		file render.FileSummary
		want string
	}{
		{render.FileSummary{Mimetype: "image/png"}, "png"},
		{render.FileSummary{Filetype: "jpeg"}, "jpg"},
		{render.FileSummary{Mimetype: "image/webp"}, "webp"},
		{render.FileSummary{Mimetype: "text/plain"}, "txt"},
		{render.FileSummary{Mimetype: "text/markdown"}, "md"},
		{render.FileSummary{Mimetype: "application/json"}, "json"},
		{render.FileSummary{Name: "report.PDF"}, "pdf"}, // filename fallback, lowercased
		{render.FileSummary{Title: "notes.txt"}, "txt"}, // title fallback
		{render.FileSummary{Mimetype: "application/x-unknown"}, ""},
		{render.FileSummary{}, ""},
	}
	for _, tc := range cases {
		if got := InferFileExt(tc.file); got != tc.want {
			t.Errorf("InferFileExt(%+v) = %q, want %q", tc.file, got, tc.want)
		}
	}
}
