package render

import (
	"strings"
	"testing"
)

// Round-trip / cross-renderer coverage for the inline pipeline. The hub format
// is rich_text blocks: standard Markdown and slack-mrkdwn both parse INTO
// blocks, and blocks serialize back to mrkdwn (which converts to Markdown).
//
// NOTE — live probe of Slack (2026-06): posting a rich_text block whose link,
// user, channel, emoji and broadcast elements each carried a style, then
// reading the stored message back, showed Slack PRESERVES style on every inline
// element type (not just text). The only transform Slack applies is enriching
// an emoji element with a resolved `unicode` field — an addition, not a drop.
// So our serializer must keep style on non-text tokens too (applyMrkdwnStyle),
// which these tests pin.

func sectionBlock(els ...map[string]any) map[string]any {
	anyEls := make([]any, len(els))
	for i, e := range els {
		anyEls[i] = e
	}
	return map[string]any{"type": "rich_text", "elements": []any{
		map[string]any{"type": "rich_text_section", "elements": anyEls},
	}}
}

func italic() map[string]any { return map[string]any{"italic": true} }
func bold() map[string]any   { return map[string]any{"bold": true} }

// blocks → mrkdwn must wrap styled non-text tokens in emphasis, not drop it.
func TestBlocksToMrkdwnStylesTokens(t *testing.T) {
	block := sectionBlock(
		map[string]any{"type": "link", "url": "https://e.com", "text": "x", "style": italic()},
		map[string]any{"type": "text", "text": " "},
		map[string]any{"type": "user", "user_id": "U12345678", "style": bold()},
		map[string]any{"type": "text", "text": " "},
		map[string]any{"type": "emoji", "name": "wave", "style": italic()},
	)
	got := richTextBlockToMrkdwn(block)
	for _, want := range []string{"_<https://e.com|x>_", "*<@U12345678>*", "_:wave:_"} {
		if !strings.Contains(got, want) {
			t.Errorf("serialized mrkdwn %q missing %q", got, want)
		}
	}
}

// blocks → mrkdwn → blocks preserves a styled link (idempotence of our own
// serializer + parser, the loop the style-drop bug broke).
func TestRoundTripBlocksMrkdwnBlocks(t *testing.T) {
	block := sectionBlock(
		map[string]any{"type": "text", "text": "see ", "style": italic()},
		map[string]any{"type": "link", "url": "https://e.com", "text": "x", "style": italic()},
	)
	reparsed := ParseInlineElements(richTextBlockToMrkdwn(block))

	var link *InlineElement
	for i := range reparsed {
		if reparsed[i].Type == "link" {
			link = &reparsed[i]
		}
	}
	if link == nil || link.URL != "https://e.com" || link.Text != "x" {
		t.Fatalf("link lost through blocks→mrkdwn→blocks: %+v", reparsed)
	}
	if link.Style == nil || !link.Style.Italic {
		t.Errorf("italic lost through round-trip: %+v", link.Style)
	}
}

// tests-as-documentation: how a styled link in mrkdwn surfaces as Markdown.
func TestMrkdwnToMarkdownStyledLink(t *testing.T) {
	if got, want := MrkdwnToMarkdown("_<https://e.com|x>_", false), "_[x](https://e.com)_"; got != want {
		t.Errorf("MrkdwnToMarkdown = %q, want %q", got, want)
	}
}
