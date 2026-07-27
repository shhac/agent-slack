package render

import (
	"encoding/json"
	"reflect"
	"testing"
)

// jsonEqual compares got (marshalled) against a want JSON literal, ignoring
// key order, so tests read like the TS expectations.
func jsonEqual(t *testing.T, name string, got any, want string) {
	t.Helper()
	gotBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal: %v", name, err)
	}
	var gotVal, wantVal any
	if err := json.Unmarshal(gotBytes, &gotVal); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := json.Unmarshal([]byte(want), &wantVal); err != nil {
		t.Fatalf("%s: bad want fixture: %v", name, err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("%s:\n got %s\nwant %s", name, gotBytes, want)
	}
}

func TestParseInlineElements(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "Hello world",
			`[{"type":"text","text":"Hello world"}]`},
		{"bold", "Hello *world*!",
			`[{"type":"text","text":"Hello "},{"type":"text","text":"world","style":{"bold":true}},{"type":"text","text":"!"}]`},
		{"italic", "This is _important_",
			`[{"type":"text","text":"This is "},{"type":"text","text":"important","style":{"italic":true}}]`},
		{"strike", "~done~",
			`[{"type":"text","text":"done","style":{"strike":true}}]`},
		{"code", "Run `npm install`",
			`[{"type":"text","text":"Run "},{"type":"text","text":"npm install","style":{"code":true}}]`},
		{"emoji shortcode", "Launch :rocket: now",
			`[{"type":"text","text":"Launch "},{"type":"emoji","name":"rocket"},{"type":"text","text":" now"}]`},
		{"emoji with underscores is not italic", ":white_check_mark: all clear",
			`[{"type":"emoji","name":"white_check_mark"},{"type":"text","text":" all clear"}]`},
		{"time-like colons are not emoji", "Time 12:30:00",
			`[{"type":"text","text":"Time 12:30:00"}]`},
		{"labeled link", "Visit <https://example.com|Example>",
			`[{"type":"text","text":"Visit "},{"type":"link","url":"https://example.com","text":"Example"}]`},
		{"bare link", "See <https://example.com>",
			`[{"type":"text","text":"See "},{"type":"link","url":"https://example.com"}]`},
		{"mailto link", "Email <mailto:bob@example.com|Bob>",
			`[{"type":"text","text":"Email "},{"type":"link","url":"mailto:bob@example.com","text":"Bob"}]`},
		{"non-url angle text preserved", "Use <fix>",
			`[{"type":"text","text":"Use "},{"type":"text","text":"<fix>"}]`},
		{"non-url labeled angle text preserved", "Use <fix|label>",
			`[{"type":"text","text":"Use "},{"type":"text","text":"<fix|label>"}]`},
		{"channel mention with label", "See <#C12345678|general>",
			`[{"type":"text","text":"See "},{"type":"channel","channel_id":"C12345678"}]`},
		{"bare channel mention", "See <#C12345678>",
			`[{"type":"text","text":"See "},{"type":"channel","channel_id":"C12345678"}]`},
		{"usergroup mention", "Ping <!subteam^S12345678|@team>",
			`[{"type":"text","text":"Ping "},{"type":"usergroup","usergroup_id":"S12345678"}]`},
		{"user token", "hi <@U123456A>",
			`[{"type":"text","text":"hi "},{"type":"user","user_id":"U123456A"}]`},
		{"broadcast token", "<!here> we go",
			`[{"type":"broadcast","range":"here"},{"type":"text","text":" we go"}]`},
		{"bare user mention", "@U05BRPTKL6A heads up",
			`[{"type":"user","user_id":"U05BRPTKL6A"},{"type":"text","text":" heads up"}]`},
		{"bare broadcast", "cc @channel now",
			`[{"type":"text","text":"cc "},{"type":"broadcast","range":"channel"},{"type":"text","text":" now"}]`},
		{"short bare id stays text", "@U1234 hello",
			`[{"type":"text","text":"@U1234 hello"}]`},
		{"email-like @ stays text", "user@Udomain.com",
			`[{"type":"text","text":"user@Udomain.com"}]`},
		// The TS version returns one empty text element; we keep that shape
		// but omitempty drops the empty text field from JSON.
		{"empty string", "",
			`[{"type":"text"}]`},
	}
	for _, tc := range cases {
		jsonEqual(t, tc.name, ParseInlineElements(tc.input), tc.want)
	}
}

// Inline tokens inside a slack-mrkdwn emphasis span must parse into real
// elements (carrying the span's style), not be emitted as literal text — the
// bug where an italicized line's <url|label> rendered as text.
func TestParseInlineElementsEmphasisRecursesTokens(t *testing.T) {
	link := func(els []InlineElement) *InlineElement {
		for i := range els {
			if els[i].Type == "link" {
				return &els[i]
			}
		}
		return nil
	}

	got := ParseInlineElements("_see <https://example.com|here> now_")
	l := link(got)
	if l == nil {
		t.Fatalf("link inside italic stayed literal: %+v", got)
	}
	if l.URL != "https://example.com" || l.Text != "here" {
		t.Errorf("link = %+v", l)
	}
	if l.Style == nil || !l.Style.Italic {
		t.Errorf("link should carry the enclosing italic style: %+v", l.Style)
	}

	// A mention inside bold becomes a user element (not literal), styled bold.
	bold := ParseInlineElements("*ping <@U12345678>*")
	var mention *InlineElement
	for i := range bold {
		if bold[i].Type == "user" {
			mention = &bold[i]
		}
	}
	if mention == nil || mention.UserID != "U12345678" {
		t.Fatalf("mention inside bold stayed literal: %+v", bold)
	}
	if mention.Style == nil || !mention.Style.Bold {
		t.Errorf("mention should carry the enclosing bold style: %+v", mention.Style)
	}

	// Nested emphasis combines styles.
	nested := ParseInlineElements("_a *b* c_")
	for _, el := range nested {
		if el.Text == "b" && (el.Style == nil || !el.Style.Bold || !el.Style.Italic) {
			t.Errorf("nested *b* inside _…_ should be bold+italic: %+v", el.Style)
		}
	}
}

// Emphasis must style inline tokens (link/mention/emoji) identically in BOTH
// dialects — standard Markdown and slack-mrkdwn. The in-emphasis link bug was a
// parity break: slack-mrkdwn dropped the token entirely, and standard Markdown
// kept the link but not its style. A cross-dialect parity test catches both.
func TestEmphasisStylesTokensInBothDialects(t *testing.T) {
	findType := func(els []InlineElement, typ string) *InlineElement {
		for i := range els {
			if els[i].Type == typ {
				return &els[i]
			}
		}
		return nil
	}
	cases := []struct{ name, markdown, mrkdwn, typ string }{
		{"link", "_[x](https://e.com)_", "_<https://e.com|x>_", "link"},
		{"mention", "_<@U12345678>_", "_<@U12345678>_", "user"},
		{"emoji", "_:wave:_", "_:wave:_", "emoji"},
		{"channel", "_<#C12345678|gen>_", "_<#C12345678|gen>_", "channel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := findType(ParseMarkdownInline(tc.markdown), tc.typ)
			sm := findType(ParseInlineElements(tc.mrkdwn), tc.typ)
			if md == nil {
				t.Fatalf("standard Markdown dropped the %s token: %q", tc.typ, tc.markdown)
			}
			if sm == nil {
				t.Fatalf("slack-mrkdwn dropped the %s token: %q", tc.typ, tc.mrkdwn)
			}
			if md.Style == nil || !md.Style.Italic {
				t.Errorf("standard Markdown: %s inside _…_ not italic (style=%+v)", tc.typ, md.Style)
			}
			if sm.Style == nil || !sm.Style.Italic {
				t.Errorf("slack-mrkdwn: %s inside _…_ not italic (style=%+v)", tc.typ, sm.Style)
			}
		})
	}
}
