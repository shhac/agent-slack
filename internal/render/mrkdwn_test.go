package render

import "testing"

func TestMrkdwnToMarkdown(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"labeled link", "See <https://example.com|this>.", "See [this](https://example.com)."},
		{"bare link", "See <https://example.com>.", "See https://example.com."},
		{"user and channel mentions", "Hi <@U12345> in <#C1|general>", "Hi @U12345 in #general"},
		{"user mention with nick", "Hi <@U12345|nick>", "Hi @nick"},
		{"special mentions", "<!here> and <!channel>", "@here and @channel"},
		{"emoji shortcodes to unicode", "Ship it :rocket:", "Ship it 🚀"},
		{"unknown shortcode untouched", "keep :not_a_real_emoji_xyz: as-is", "keep :not_a_real_emoji_xyz: as-is"},
		{"html entities decoded", "a &lt;tag&gt; &amp; more", "a <tag> & more"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		if got := MrkdwnToMarkdown(tc.input); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
