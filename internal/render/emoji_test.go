package render

import "testing"

func TestEmojifyShortcodes(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"known", "Launch :rocket: now", "Launch 🚀 now"},
		{"plus-one", ":+1:", "👍"},
		{"check mark", ":white_check_mark: all clear", "✅ all clear"},
		{"unknown left alone", ":unknown_thing:", ":unknown_thing:"},
		{"adjacent shortcodes", ":rocket::rocket:", "🚀🚀"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		if got := EmojifyShortcodes(tc.input); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeReactionName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"shortcode", ":rocket:", "rocket"},
		{"shortcode plus-one", ":+1:", "+1"},
		{"raw name", "rocket", "rocket"},
		{"raw plus-one", "+1", "+1"},
		{"raw with underscores", "white_check_mark", "white_check_mark"},
		{"unicode emoji", "🚀", "rocket"},
		{"unicode thumbs up", "👍", "+1"},
		{"unicode with skin tone", "👍🏽", "+1"},
		{"unicode with variation selector", "❤️", "heart"},
		{"surrounding whitespace", " :rocket: ", "rocket"},
	}
	for _, tc := range cases {
		got, err := NormalizeReactionName(tc.input)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeReactionNameErrors(t *testing.T) {
	for _, input := range []string{"", "   ", "not an emoji!", "::"} {
		if _, err := NormalizeReactionName(input); err == nil {
			t.Errorf("expected error for %q", input)
		}
	}
}

// The wire form Slack sends when a reactor has a skin tone set. One normalizer
// serves both `message react` input and `message await --reaction`, so a
// skin-toned reaction must reduce to the same name as a plain one.
func TestNormalizeReactionNameStripsSkinTone(t *testing.T) {
	cases := map[string]string{
		"+1::skin-tone-3":    "+1",
		"+1":                 "+1",
		":white_check_mark:": "white_check_mark",
		"🚀":                  "rocket",
	}
	for input, want := range cases {
		got, err := NormalizeReactionName(input)
		if err != nil {
			t.Errorf("NormalizeReactionName(%q) errored: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeReactionName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStripSkinToneIsTotal(t *testing.T) {
	if got := StripSkinTone("eyes"); got != "eyes" {
		t.Errorf("StripSkinTone(eyes) = %q", got)
	}
	if got := StripSkinTone(" +1::skin-tone-5 "); got != "+1" {
		t.Errorf("StripSkinTone = %q", got)
	}
}
