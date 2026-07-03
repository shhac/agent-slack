package render

import (
	"strings"
	"testing"
)

func newRenderState() *renderState { return &renderState{} }

func TestRenderNormalAttachmentEdges(t *testing.T) {
	// title_link without title still surfaces the link.
	got := renderNormalAttachment(map[string]any{"title_link": "https://x.example"}, newRenderState())
	if got != "https://x.example" {
		t.Errorf("link-only: %q", got)
	}

	// fallback is used ONLY when nothing else rendered.
	got = renderNormalAttachment(map[string]any{"fallback": "fb", "text": "real"}, newRenderState())
	if strings.Contains(got, "fb") || !strings.Contains(got, "real") {
		t.Errorf("fallback must not show beside real content: %q", got)
	}
	if got = renderNormalAttachment(map[string]any{"fallback": "fb"}, newRenderState()); got != "fb" {
		t.Errorf("fallback-only: %q", got)
	}

	// Empty attachment renders to nothing (not an empty chunk).
	if got = renderNormalAttachment(map[string]any{}, newRenderState()); got != "" {
		t.Errorf("empty attachment: %q", got)
	}
}

func TestRenderSharedAttachmentFallbackChain(t *testing.T) {
	// No message_blocks, no nested attachments → quoted text.
	got := renderSharedAttachment(map[string]any{"is_share": true, "text": "original words"}, newRenderState())
	if !strings.Contains(got, "*Forwarded message*") || !strings.Contains(got, "> original words") {
		t.Errorf("text fallback: %q", got)
	}

	// Header variants: author + source link.
	got = renderSharedAttachment(map[string]any{
		"is_share": true, "author_name": "Alice", "author_link": "https://a.example", "from_url": "https://o.example",
	}, newRenderState())
	if !strings.Contains(got, "<https://a.example|Alice>") || !strings.Contains(got, "<https://o.example|original>") {
		t.Errorf("header: %q", got)
	}
}

func TestAttachmentDepthLimit(t *testing.T) {
	// Build a chain nested one past the limit; content above the cutoff
	// renders, the deepest level is dropped rather than recursing forever.
	innermost := map[string]any{"text": "too-deep"}
	chain := innermost
	for i := 0; i < maxAttachmentDepth; i++ {
		chain = map[string]any{"text": "level", "attachments": []any{chain}}
	}
	got := mrkdwnFromAttachments([]any{chain}, newRenderState())
	if strings.Contains(got, "too-deep") {
		t.Errorf("content beyond maxAttachmentDepth must be dropped:\n%s", got)
	}
	if !strings.Contains(got, "level") {
		t.Errorf("content inside the limit must render:\n%s", got)
	}
}

func TestAttachmentCycleSafety(t *testing.T) {
	// An attachment that contains itself must not hang or duplicate.
	a := map[string]any{"text": "cyclic"}
	a["attachments"] = []any{a}
	got := mrkdwnFromAttachments([]any{a}, newRenderState())
	if strings.Count(got, "cyclic") != 1 {
		t.Errorf("cycle should render once: %q", got)
	}
}

func TestUniqueTexts(t *testing.T) {
	got := uniqueTexts([]string{" a ", "", "a", "b", "a"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v", got)
	}
}
