package render

import (
	"strings"
	"testing"
)

// Nesting behavior for markdown lists → rich_text_list blocks: depth on
// `indent`, numbering continued across sub-lists via `offset`.

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

// listShape is one expected rich_text_list run.
type listShape struct {
	style  string
	indent int
	offset int
	items  int
}

func assertListShapes(t *testing.T, src string, want []listShape) {
	t.Helper()
	lists := listElements(t, TextToRichTextBlocks(src, RichTextOptions{}))
	if len(lists) != len(want) {
		t.Fatalf("got %d list runs, want %d", len(lists), len(want))
	}
	for i, w := range want {
		got := lists[i]
		if got.Style != w.style || got.Indent != w.indent || got.Offset != w.offset || len(got.Elements) != w.items {
			t.Errorf("run %d = {style:%s indent:%d offset:%d items:%d}, want {style:%s indent:%d offset:%d items:%d}",
				i, got.Style, got.Indent, got.Offset, len(got.Elements), w.style, w.indent, w.offset, w.items)
		}
	}
}

// A sub-list of a DIFFERENT style must still nest, and must not restart the
// numbering of the ordered list it interrupts. Slack has no nested-list
// container, so this is entirely carried by indent + offset.
func TestTextToRichTextBlocksNestedMixedStyles(t *testing.T) {
	assertListShapes(t, "1. One\n    - a\n    - b\n2. Two\n    - c\n3. Three\n4. Four", []listShape{
		{"ordered", 0, 0, 1},
		{"bullet", 1, 0, 2},
		{"ordered", 0, 1, 1},
		{"bullet", 1, 0, 1},
		{"ordered", 0, 2, 2},
	})
}

func TestTextToRichTextBlocksNestsBeyondOneLevel(t *testing.T) {
	assertListShapes(t, "- a\n  - b\n    - c\n      - d", []listShape{
		{"bullet", 0, 0, 1},
		{"bullet", 1, 0, 1},
		{"bullet", 2, 0, 1},
		{"bullet", 3, 0, 1},
	})
}

// Tab-indented sources nest like space-indented ones.
func TestTextToRichTextBlocksTabIndent(t *testing.T) {
	assertListShapes(t, "1. One\n\t- a\n2. Two", []listShape{
		{"ordered", 0, 0, 1},
		{"bullet", 1, 0, 1},
		{"ordered", 0, 1, 1},
	})
}

// A second sub-list under a later parent restarts at 1 rather than resuming the
// first sub-list's count — coming back up closes the deeper level.
func TestTextToRichTextBlocksNestedOrderedRestartsPerParent(t *testing.T) {
	assertListShapes(t, "1. One\n    1. a\n    2. b\n2. Two\n    1. c", []listShape{
		{"ordered", 0, 0, 1},
		{"ordered", 1, 0, 2},
		{"ordered", 0, 1, 1},
		{"ordered", 1, 0, 1},
	})
}

// A blank line between items makes one loose list, not two lists that each
// restart at 1.
func TestTextToRichTextBlocksLooseListKeepsNumbering(t *testing.T) {
	assertListShapes(t, "1. One\n\n2. Two\n\n3. Three", []listShape{{"ordered", 0, 0, 3}})
}

// A non-list line does end the list, so the next list starts fresh.
func TestTextToRichTextBlocksParagraphResetsNumbering(t *testing.T) {
	assertListShapes(t, "1. One\n2. Two\n\nAside\n\n1. Fresh", []listShape{
		{"ordered", 0, 0, 2},
		{"ordered", 0, 0, 1},
	})
}

// CommonMark honours the first number as the list's start; later numbers are
// positional. "5." therefore means offset 4, and lazy "1. 1. 1." still counts up.
func TestTextToRichTextBlocksHonoursFirstNumber(t *testing.T) {
	assertListShapes(t, "5. Five\n6. Six", []listShape{{"ordered", 0, 4, 2}})
	assertListShapes(t, "1. One\n1. Two\n1. Three", []listShape{{"ordered", 0, 0, 3}})
}

// Outdenting to a width that was never opened lands on the nearest enclosing
// level rather than inventing one, so "mid" joins the top-level run.
func TestTextToRichTextBlocksOutdentToUnopenedWidth(t *testing.T) {
	assertListShapes(t, "- top1\n    - deep\n  - mid\n- top2", []listShape{
		{"bullet", 0, 0, 1},
		{"bullet", 1, 0, 1},
		{"bullet", 0, 0, 2}, // "mid" loses its nesting and joins "top2"
	})
}

// An indented first item has no parent to nest under, so it opens depth 0 and
// a later flush-left item belongs to the same run.
func TestTextToRichTextBlocksIndentedFirstItemIsDepthZero(t *testing.T) {
	assertListShapes(t, "  - orphan\n- top", []listShape{{"bullet", 0, 0, 2}})
}

// Swapping style at the same depth splits the run but keeps counting, which is
// also what the source says: CommonMark reads "2." as a list starting at two.
func TestTextToRichTextBlocksSameDepthStyleSwitch(t *testing.T) {
	assertListShapes(t, "1. One\n- x\n2. Two", []listShape{
		{"ordered", 0, 0, 1},
		{"bullet", 0, 0, 1},
		{"ordered", 0, 1, 1},
	})
}

// ")" is accepted as an ordered delimiter and nests like "."; Slack has no
// delimiter field, so both normalize to "." on the way back out.
func TestTextToRichTextBlocksParenDelimiterNests(t *testing.T) {
	assertListShapes(t, "1) One\n    - a\n2) Two", []listShape{
		{"ordered", 0, 0, 1},
		{"bullet", 1, 0, 1},
		{"ordered", 0, 1, 1},
	})
}

// Nesting past maxListIndent collapses onto the deepest supported level as ONE
// run. Emitting separate runs that share an indent would read as un-nested
// siblings — the very flattening the indent is there to prevent.
func TestTextToRichTextBlocksIndentCapCollapsesIntoOneRun(t *testing.T) {
	var src strings.Builder
	for depth := range maxListIndent + 3 {
		src.WriteString(strings.Repeat("  ", depth) + "- item\n")
	}
	lists := listElements(t, TextToRichTextBlocks(src.String(), RichTextOptions{}))

	if len(lists) != maxListIndent+1 {
		t.Fatalf("got %d runs, want %d (one per level up to the cap)", len(lists), maxListIndent+1)
	}
	deepest := lists[len(lists)-1]
	if deepest.Indent != maxListIndent {
		t.Errorf("deepest indent = %d, want %d", deepest.Indent, maxListIndent)
	}
	if len(deepest.Elements) != 3 {
		t.Errorf("deepest run has %d items, want the 3 capped levels merged", len(deepest.Elements))
	}
}
