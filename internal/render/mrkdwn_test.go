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
		{"bold to double asterisk", "this is *bold*", "this is **bold**"},
		{"strike to double tilde", "this is ~gone~", "this is ~~gone~~"},
		{"italic and underline unchanged", "_italic_ and __under__", "_italic_ and __under__"},
		{"emphasis with a link", "*bold* and <https://x.com|link>", "**bold** and [link](https://x.com)"},
		{"delimiters in code span untouched", "use `a*b` and `c~d`", "use `a*b` and `c~d`"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		if got := MrkdwnToMarkdown(tc.input, false); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMrkdwnToMarkdownSlackDialect(t *testing.T) {
	// The inbound opt-out returns the native Slack mrkdwn unchanged.
	in := "this is *bold* with <https://x.com|link> and <@U12345> :rocket:"
	if got := MrkdwnToMarkdown(in, true); got != in {
		t.Errorf("slack dialect should be unchanged: got %q", got)
	}
}
