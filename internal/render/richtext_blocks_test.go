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
