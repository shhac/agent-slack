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
		{"mixed angle text and bold", "Use <fix|label> and *bold*",
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
		{"channel mention", "See <#C12345678|general>",
			`[{"type":"rich_text_section","elements":[
				{"type":"text","text":"See "},
				{"type":"channel","channel_id":"C12345678"},
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
	blocks := TextToRichTextBlocks("- *Bold item*\n- Normal item", RichTextOptions{})
	lists := listElements(t, blocks)
	if len(lists) != 1 {
		t.Fatalf("expected 1 list, got %d", len(lists))
	}
	jsonEqual(t, "bold list item", lists[0].Elements[0],
		`{"type":"rich_text_section","elements":[{"type":"text","text":"Bold item","style":{"bold":true}}]}`)
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
