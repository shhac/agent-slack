package render

import (
	"encoding/json"
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

// blocksAsJSON marshals emitted blocks and decodes them the way an inbound
// Slack payload arrives, so the inbound renderer is exercised across the real
// JSON boundary (and the struct tags are pinned along with it).
func blocksAsJSON(t *testing.T, blocks []RichTextBlock) any {
	t.Helper()
	raw, err := json.Marshal(blocks[0])
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal blocks: %v", err)
	}
	return decoded
}

// A nested list survives Markdown → rich_text → Markdown unchanged. This is the
// loop that was broken in both directions: sending flattened the nesting and
// restarted numbering, and reading back dropped indent/offset so even a
// correctly nested message (hand-built via `api call`) came back flat.
func TestRoundTripNestedListMarkdown(t *testing.T) {
	src := "1. One\n    - a\n    - b\n2. Two\n    - c\n3. Three\n4. Four"

	blocks := TextToRichTextBlocks(src, RichTextOptions{})
	got := MrkdwnToMarkdown(richTextBlockToMrkdwn(blocksAsJSON(t, blocks)), false)

	if got != src {
		t.Errorf("round-trip changed the list:\n got %q\nwant %q", got, src)
	}
}

// Deeper nesting and nested ordered lists round-trip too.
func TestRoundTripDeepNestedListMarkdown(t *testing.T) {
	src := "- top\n    - mid\n        - deep\n- back"

	blocks := TextToRichTextBlocks(src, RichTextOptions{})
	got := MrkdwnToMarkdown(richTextBlockToMrkdwn(blocksAsJSON(t, blocks)), false)

	if got != src {
		t.Errorf("round-trip changed the list:\n got %q\nwant %q", got, src)
	}
}

// Reading a message somebody else authored with indent/offset must honour both
// rather than flattening it — the gap that made `message list` show a correctly
// nested message identically to a flattened one.
func TestBlocksToMrkdwnHonoursIndentAndOffset(t *testing.T) {
	list := func(style string, indent, offset int, texts ...string) map[string]any {
		items := make([]any, len(texts))
		for i, txt := range texts {
			items[i] = map[string]any{"type": "rich_text_section", "elements": []any{
				map[string]any{"type": "text", "text": txt},
			}}
		}
		el := map[string]any{"type": "rich_text_list", "style": style, "indent": indent, "elements": items}
		if offset != 0 {
			el["offset"] = offset
		}
		return el
	}
	block := map[string]any{"type": "rich_text", "elements": []any{
		list("ordered", 0, 0, "One"),
		list("bullet", 1, 0, "a"),
		list("ordered", 0, 1, "Two"),
	}}

	want := "1. One\n    - a\n2. Two"
	if got := richTextBlockToMrkdwn(block); got != want {
		t.Errorf("blocks → mrkdwn = %q, want %q", got, want)
	}
}

// An empty list item keeps its position on read. Dropping it used to shift
// every ordered item below it up by one — a silent renumbering, visible only
// after the message had already been sent.
func TestRoundTripEmptyOrderedItemKeepsNumbering(t *testing.T) {
	blocks := TextToRichTextBlocks("1. One\n2. \n3. Three", RichTextOptions{})
	got := MrkdwnToMarkdown(richTextBlockToMrkdwn(blocksAsJSON(t, blocks)), false)

	if want := "1. One\n2.\n3. Three"; got != want {
		t.Errorf("empty item renumbered the list:\n got %q\nwant %q", got, want)
	}
}

// Nesting past the indent cap must not come back as flat siblings.
func TestRoundTripBeyondIndentCapStaysNested(t *testing.T) {
	var src strings.Builder
	for depth := range maxListIndent + 2 {
		src.WriteString(strings.Repeat("  ", depth) + "- item\n")
	}
	blocks := TextToRichTextBlocks(src.String(), RichTextOptions{})
	got := MrkdwnToMarkdown(richTextBlockToMrkdwn(blocksAsJSON(t, blocks)), false)

	lines := strings.Split(got, "\n")
	if len(lines) != maxListIndent+2 {
		t.Fatalf("got %d lines, want %d", len(lines), maxListIndent+2)
	}
	// Every level up to the cap keeps a distinct indent; the rest share the deepest.
	for i, line := range lines {
		want := strings.Repeat(listIndentUnit, min(i, maxListIndent)) + "- item"
		if line != want {
			t.Errorf("line %d = %q, want %q", i, line, want)
		}
	}
}
