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
