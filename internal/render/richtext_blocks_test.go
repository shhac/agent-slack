package render

import "testing"

func TestRichTextBlocksForText(t *testing.T) {
	// Plain text → exactly one rich_text block (TextToRichTextBlocks returns nil here).
	if got := TextToRichTextBlocks("hello world", RichTextOptions{}); got != nil {
		t.Fatalf("precondition: plain text should yield nil blocks, got %v", got)
	}
	plain := RichTextBlocksForText("hello world")
	if len(plain) != 1 || plain[0].Type != "rich_text" {
		t.Errorf("plain text blocks = %+v", plain)
	}
	// Structured text → delegates to TextToRichTextBlocks (non-empty).
	if got := RichTextBlocksForText("- one\n- two"); len(got) == 0 {
		t.Error("structured text should produce blocks")
	}
}
