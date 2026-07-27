package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRichTextBlocksForText(t *testing.T) {
	// Plain text → exactly one rich_text block (TextToRichTextBlocks returns nil here).
	if got := TextToRichTextBlocks("hello world", RichTextOptions{}); got != nil {
		t.Fatalf("precondition: plain text should yield nil blocks, got %v", got)
	}
	plain := RichTextBlocksForText("hello world", RichTextOptions{})
	if len(plain) != 1 || plain[0].Type != "rich_text" {
		t.Errorf("plain text blocks = %+v", plain)
	}
	// The text must actually survive into the block (drafts have no text fallback).
	if raw, _ := json.Marshal(plain); !strings.Contains(string(raw), "hello world") {
		t.Errorf("plain block dropped the text: %s", raw)
	}

	// Inline formatting (a mention) round-trips through the IncludeInlineFormatting path.
	if raw, _ := json.Marshal(RichTextBlocksForText("hi <@U12345678>", RichTextOptions{})); !strings.Contains(string(raw), "U12345678") {
		t.Errorf("inline content lost: %s", raw)
	}

	// Structured text → delegates to TextToRichTextBlocks (non-empty).
	if got := RichTextBlocksForText("- one\n- two", RichTextOptions{}); len(got) == 0 {
		t.Error("structured text should produce blocks")
	}
}

// The synthesized plain-text fallback block has an exact shape (one rich_text →
// rich_text_section → single text element). Lock it: drafts have no text
// fallback, so any change to this structure changes what users see.
func TestRichTextBlocksForTextPlainShape(t *testing.T) {
	const want = `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hello world"}]}]}]`
	got, _ := json.Marshal(RichTextBlocksForText("hello world", RichTextOptions{}))
	if string(got) != want {
		t.Errorf("plain fallback shape:\n got %s\nwant %s", got, want)
	}
}

// RenderOutbound is the one place the dialect→(blocks, text) rule lives for
// send/edit. Pin all three contract cases directly (CLI tests only exercise it
// indirectly): plain Markdown stays a plain text field; Markdown formatting
// moves into blocks with a flattened fallback; Slack-mrkdwn keeps inline
// formatting in the native text field (no blocks).
func TestRenderOutbound(t *testing.T) {
	// Plain Markdown → no blocks, fallback unchanged.
	if blocks, fallback := RenderOutbound("Hello world", false); len(blocks) != 0 || fallback != "Hello world" {
		t.Errorf("plain markdown: blocks=%v fallback=%q", blocks, fallback)
	}

	// Markdown formatting → blocks carry the style; fallback flattened (no **).
	blocks, fallback := RenderOutbound("**bold**", false)
	if len(blocks) == 0 {
		t.Fatal("markdown bold should produce blocks")
	}
	if raw, _ := json.Marshal(blocks); !strings.Contains(string(raw), `"bold":true`) || strings.Contains(string(raw), "**") {
		t.Errorf("markdown bold blocks = %s", raw)
	}
	if fallback != "bold" {
		t.Errorf("markdown bold fallback = %q, want flattened 'bold'", fallback)
	}

	// Slack mrkdwn → inline formatting stays in the native text field, no blocks.
	if blocks, fallback := RenderOutbound("*bold*", true); len(blocks) != 0 || fallback != "*bold*" {
		t.Errorf("slack mrkdwn: blocks=%v fallback=%q", blocks, fallback)
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
