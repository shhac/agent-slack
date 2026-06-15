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

func TestTextToRichTextBlocksNil(t *testing.T) {
	cases := []struct {
		name string
		text string
		opts RichTextOptions
	}{
		{"plain text", "Hello world", RichTextOptions{}},
		{"inline-only without option", "Visit <https://example.com|Example>", RichTextOptions{}},
		{"non-url angle text", "Use <fix>", RichTextOptions{IncludeInlineFormatting: true}},
		{"non-url labeled angle text", "Use <fix|label>", RichTextOptions{IncludeInlineFormatting: true}},
		// A mention/channel alone renders in the text field, so it does not force
		// blocks even with IncludeInlineFormatting (only styling/links do).
		{"channel mention only", "See <#C12345678|general>", RichTextOptions{IncludeInlineFormatting: true}},
		{"user mention only", "ping <@U12345678>", RichTextOptions{IncludeInlineFormatting: true}},
	}
	for _, tc := range cases {
		if got := TextToRichTextBlocks(tc.text, tc.opts); got != nil {
			t.Errorf("%s: expected nil, got %+v", tc.name, got)
		}
	}
}

func TestTextToRichTextBlocksInlineFormatting(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"mixed angle text and bold", "Use <fix|label> and **bold**",
			`[{"type":"rich_text_section","elements":[
				{"type":"text","text":"Use "},
				{"type":"text","text":"<fix|label>"},
				{"type":"text","text":" and "},
				{"type":"text","text":"bold","style":{"bold":true}},
				{"type":"text","text":"\n"}
			]}]`},
		{"labeled link", "Visit <https://example.com|Example>",
			`[{"type":"rich_text_section","elements":[
				{"type":"text","text":"Visit "},
				{"type":"link","url":"https://example.com","text":"Example"},
				{"type":"text","text":"\n"}
			]}]`},
		{"mailto link", "Email <mailto:bob@example.com|Bob>",
			`[{"type":"rich_text_section","elements":[
				{"type":"text","text":"Email "},
				{"type":"link","url":"mailto:bob@example.com","text":"Bob"},
				{"type":"text","text":"\n"}
			]}]`},
	}
	for _, tc := range cases {
		got := TextToRichTextBlocks(tc.text, RichTextOptions{IncludeInlineFormatting: true})
		if got == nil {
			t.Errorf("%s: expected blocks, got nil", tc.name)
			continue
		}
		jsonEqual(t, tc.name, got[0].Elements, tc.want)
	}
}

func listElements(t *testing.T, blocks []RichTextBlock) []RichTextElement {
	t.Helper()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	var lists []RichTextElement
	for _, el := range blocks[0].Elements {
		if el.Type == "rich_text_list" {
			lists = append(lists, el)
		}
	}
	return lists
}

func TestTextToRichTextBlocksBulletList(t *testing.T) {
	blocks := TextToRichTextBlocks("- Item 1\n- Item 2\n- Item 3", RichTextOptions{})
	lists := listElements(t, blocks)
	if len(lists) != 1 {
		t.Fatalf("expected 1 list, got %d", len(lists))
	}
	if len(lists[0].Elements) != 3 {
		t.Errorf("expected 3 items, got %d", len(lists[0].Elements))
	}
	if lists[0].Style != "bullet" {
		t.Errorf("style = %q", lists[0].Style)
	}
}

func TestTextToRichTextBlocksBulletCharacter(t *testing.T) {
	if TextToRichTextBlocks("• Item 1\n• Item 2", RichTextOptions{}) == nil {
		t.Error("expected blocks for • bullets")
	}
}

func TestTextToRichTextBlocksSubBullets(t *testing.T) {
	blocks := TextToRichTextBlocks("- Main 1\n- Main 2\n  - Sub 2a\n  - Sub 2b\n- Main 3", RichTextOptions{})
	lists := listElements(t, blocks)
	if len(lists) != 3 { // main, sub, main
		t.Fatalf("expected 3 list runs, got %d", len(lists))
	}
	if lists[1].Indent != 1 {
		t.Errorf("sub list indent = %d, want 1", lists[1].Indent)
	}
	if lists[0].Indent != 0 || lists[2].Indent != 0 {
		t.Error("main lists should not be indented")
	}
}

func TestTextToRichTextBlocksWhiteBulletSubs(t *testing.T) {
	blocks := TextToRichTextBlocks("• Top level\n  ◦ Sub-bullet\n  ◦ Another sub", RichTextOptions{})
	lists := listElements(t, blocks)
	if len(lists) != 2 {
		t.Fatalf("expected 2 list runs, got %d", len(lists))
	}
	if lists[1].Indent != 1 {
		t.Errorf("sub list indent = %d, want 1", lists[1].Indent)
	}
}

func TestTextToRichTextBlocksMixedTextAndBullets(t *testing.T) {
	blocks := TextToRichTextBlocks("Here is a list:\n- Item 1\n- Item 2", RichTextOptions{})
	if blocks == nil {
		t.Fatal("expected blocks")
	}
	els := blocks[0].Elements
	if len(els) < 2 || els[0].Type != "rich_text_section" || els[1].Type != "rich_text_list" {
		t.Errorf("unexpected element layout: %+v", els)
	}
}

func TestTextToRichTextBlocksNumberedList(t *testing.T) {
	blocks := TextToRichTextBlocks("1. First\n2. Second\n3. Third", RichTextOptions{})
	lists := listElements(t, blocks)
	if len(lists) != 1 || lists[0].Style != "ordered" {
		t.Fatalf("expected one ordered list, got %+v", lists)
	}
}

func TestTextToRichTextBlocksBoldListItems(t *testing.T) {
	blocks := TextToRichTextBlocks("- **Bold item**\n- Normal item", RichTextOptions{})
	lists := listElements(t, blocks)
	if len(lists) != 1 {
		t.Fatalf("expected 1 list, got %d", len(lists))
	}
	jsonEqual(t, "bold list item", lists[0].Elements[0],
		`{"type":"rich_text_section","elements":[{"type":"text","text":"Bold item","style":{"bold":true}}]}`)
}

// TestTextToRichTextBlocksSlackDialect pins the --slack-markdown opt-out: with
// SlackMarkdown set, single-delimiter Slack mrkdwn is parsed (and standard
// Markdown markers are taken literally).
func TestTextToRichTextBlocksSlackDialect(t *testing.T) {
	blocks := TextToRichTextBlocks("a *bold* and _italic_ and ~struck~",
		RichTextOptions{IncludeInlineFormatting: true, SlackMarkdown: true})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	jsonEqual(t, "slack dialect", blocks[0].Elements, `[{"type":"rich_text_section","elements":[
		{"type":"text","text":"a "},
		{"type":"text","text":"bold","style":{"bold":true}},
		{"type":"text","text":" and "},
		{"type":"text","text":"italic","style":{"italic":true}},
		{"type":"text","text":" and "},
		{"type":"text","text":"struck","style":{"strike":true}},
		{"type":"text","text":"\n"}
	]}]`)
}

// TestTextToRichTextBlocksMarkdownDialect pins the default: standard Markdown.
func TestTextToRichTextBlocksMarkdownDialect(t *testing.T) {
	blocks := TextToRichTextBlocks("a **bold** and _italic_ and ~~struck~~ and __under__",
		RichTextOptions{IncludeInlineFormatting: true})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	jsonEqual(t, "markdown dialect", blocks[0].Elements, `[{"type":"rich_text_section","elements":[
		{"type":"text","text":"a "},
		{"type":"text","text":"bold","style":{"bold":true}},
		{"type":"text","text":" and "},
		{"type":"text","text":"italic","style":{"italic":true}},
		{"type":"text","text":" and "},
		{"type":"text","text":"struck","style":{"strike":true}},
		{"type":"text","text":" and "},
		{"type":"text","text":"under","style":{"underline":true}},
		{"type":"text","text":"\n"}
	]}]`)
}

func TestTextToRichTextBlocksEmojiAndChannelInItems(t *testing.T) {
	blocks := TextToRichTextBlocks(
		"Header:\n- :rocket: launch sequence\n- discuss in <#C0AHR9XAT8B>\n- :white_check_mark: all clear",
		RichTextOptions{})
	lists := listElements(t, blocks)
	if len(lists) != 1 {
		t.Fatalf("expected 1 list, got %d", len(lists))
	}
	jsonEqual(t, "items", lists[0].Elements, `[
		{"type":"rich_text_section","elements":[{"type":"emoji","name":"rocket"},{"type":"text","text":" launch sequence"}]},
		{"type":"rich_text_section","elements":[{"type":"text","text":"discuss in "},{"type":"channel","channel_id":"C0AHR9XAT8B"}]},
		{"type":"rich_text_section","elements":[{"type":"emoji","name":"white_check_mark"},{"type":"text","text":" all clear"}]}
	]`)
}

func TestTextToRichTextBlocksCodeBlock(t *testing.T) {
	blocks := TextToRichTextBlocks("- Item\n```\ncode here\n```", RichTextOptions{})
	if blocks == nil {
		t.Fatal("expected blocks")
	}
	for _, el := range blocks[0].Elements {
		if el.Type == "rich_text_preformatted" {
			jsonEqual(t, "code content", el.Elements, `[{"type":"text","text":"code here"}]`)
			return
		}
	}
	t.Error("missing rich_text_preformatted element")
}

func TestTextToRichTextBlocksBlockquote(t *testing.T) {
	blocks := TextToRichTextBlocks("- Item\n> quoted text", RichTextOptions{})
	if blocks == nil {
		t.Fatal("expected blocks")
	}
	for _, el := range blocks[0].Elements {
		if el.Type == "rich_text_quote" {
			return
		}
	}
	t.Error("missing rich_text_quote element")
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
