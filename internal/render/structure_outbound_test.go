package render

import (
	"encoding/json"
	"strings"
	"testing"
)

// The shapes Slack's own composer writes for headings and a divider (captured
// from a real draft) — the outbound parser must produce the same blocks.
func TestHeadingsAndDividerMatchTheComposer(t *testing.T) {
	got := TextToBlocks("# Hello\n## world\n### foo bar\n---\nexciting", RichTextOptions{})
	jsonEqual(t, "composer shapes", got, `[
		{"type":"header","level":1,"text":{"type":"plain_text","text":"Hello","emoji":true}},
		{"type":"header","level":2,"text":{"type":"plain_text","text":"world","emoji":true}},
		{"type":"header","level":3,"text":{"type":"plain_text","text":"foo bar","emoji":true}},
		{"type":"divider"},
		{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"exciting\n"}]}]}
	]`)
}

func TestHeadingLines(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"deeper levels clamp to the smallest heading Slack accepts", "#### Deep",
			`[{"type":"header","level":3,"text":{"type":"plain_text","text":"Deep","emoji":true}}]`},
		{"a closing run of hashes is dropped", "## Title ##",
			`[{"type":"header","level":2,"text":{"type":"plain_text","text":"Title","emoji":true}}]`},
		{"inline styles drop, since a header is already bold", "# The **big** _one_",
			`[{"type":"header","level":1,"text":{"type":"plain_text","text":"The big one","emoji":true}}]`},
		{"emoji shortcodes stay — plain_text renders them", "# Ship it :rocket:",
			`[{"type":"header","level":1,"text":{"type":"plain_text","text":"Ship it :rocket:","emoji":true}}]`},
		{"a link cannot live in a header, so the heading becomes a bold line", "## See [notes](https://example.com)",
			`[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[
				{"type":"text","text":"See ","style":{"bold":true}},
				{"type":"link","url":"https://example.com","text":"notes","style":{"bold":true}},
				{"type":"text","text":"\n"}
			]}]}]`},
		{"a mention cannot either", "# Ask <@U0FAKE01>",
			`[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[
				{"type":"text","text":"Ask ","style":{"bold":true}},
				{"type":"user","user_id":"U0FAKE01","style":{"bold":true}},
				{"type":"text","text":"\n"}
			]}]}]`},
	}
	for _, tc := range cases {
		jsonEqual(t, tc.name, TextToBlocks(tc.text, RichTextOptions{}), tc.want)
	}
}

func TestOverlongHeadingFallsBackToBold(t *testing.T) {
	long := strings.Repeat("x", maxHeaderChars+1)
	blocks := TextToBlocks("# "+long, RichTextOptions{})
	if _, ok := blocks[0].(RichTextBlock); !ok {
		t.Fatalf("a %d-char heading must not become a header (Slack rejects it): %T", len(long), blocks[0])
	}
	exact := TextToBlocks("# "+long[1:], RichTextOptions{})
	if _, ok := exact[0].(HeaderBlock); !ok {
		t.Fatalf("a %d-char heading is within the limit: %T", maxHeaderChars, exact[0])
	}
}

func TestNotHeadings(t *testing.T) {
	for _, text := range []string{"#general is where", "#", "#    ", "C# rocks", "\\# literal hash"} {
		for _, b := range TextToBlocks(text, RichTextOptions{IncludeInlineFormatting: true}) {
			if _, ok := b.(HeaderBlock); ok {
				t.Errorf("%q became a header", text)
			}
		}
	}
	// The escape leaves the hash behind, not the backslash.
	raw, _ := json.Marshal(TextToBlocks(`\# literal hash`, RichTextOptions{IncludeInlineFormatting: true}))
	if !strings.Contains(string(raw), `"# literal hash\n"`) {
		t.Errorf("escaped heading = %s", raw)
	}
}

func TestDividerLines(t *testing.T) {
	for _, text := range []string{"---", "***", "___", "-----", "* * *", "- - -", "   ---  "} {
		blocks := TextToBlocks(text, RichTextOptions{})
		if len(blocks) != 1 {
			t.Errorf("%q: %d blocks", text, len(blocks))
			continue
		}
		if _, ok := blocks[0].(DividerBlock); !ok {
			t.Errorf("%q: %T, want a divider", text, blocks[0])
		}
	}
	for _, text := range []string{"--", "-- x", "- item"} {
		for _, b := range TextToBlocks(text, RichTextOptions{}) {
			if _, ok := b.(DividerBlock); ok {
				t.Errorf("%q became a divider", text)
			}
		}
	}
	// A rule inside a code fence is code.
	if raw, _ := json.Marshal(TextToBlocks("```\n---\n```", RichTextOptions{IncludeInlineFormatting: true})); strings.Contains(string(raw), "divider") {
		t.Errorf("fenced --- became a divider: %s", raw)
	}
}

// Prose between standalone blocks gets its own rich_text block, in order, and
// the blank lines that separate them in Markdown don't open each section with
// an empty line.
func TestStandaloneBlocksSplitProse(t *testing.T) {
	got := TextToBlocks("intro\n\n# Head\n\nbody\n\n---\n\noutro", RichTextOptions{})
	jsonEqual(t, "split", got, `[
		{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"intro\n"}]}]},
		{"type":"header","level":1,"text":{"type":"plain_text","text":"Head","emoji":true}},
		{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"body\n"}]}]},
		{"type":"divider"},
		{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"outro\n"}]}]}
	]`)
}

func TestTables(t *testing.T) {
	src := strings.Join([]string{
		"| Service | Owner | p95 |",
		"|:--|---|--:|",
		"| **checkout** | <@U0FAKE01> | <https://example.com/p|412 ms> |",
		"| a \\| b | |",
		"| x | y | z | extra |",
		"after",
	}, "\n")
	blocks := TextToBlocks(src, RichTextOptions{})
	if len(blocks) != 2 {
		t.Fatalf("want table + trailing prose, got %d blocks", len(blocks))
	}
	cell := func(inner string) string {
		return `{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[` + inner + `]}]}`
	}
	text := func(s string) string { return `{"type":"text","text":"` + s + `"}` }
	jsonEqual(t, "table", blocks[0], `{
		"type":"table",
		"column_settings":[{"align":"left"},{},{"align":"right"}],
		"rows":[
			[`+cell(text("Service"))+`,`+cell(text("Owner"))+`,`+cell(text("p95"))+`],
			[`+cell(`{"type":"text","text":"checkout","style":{"bold":true}}`)+`,
			 `+cell(`{"type":"user","user_id":"U0FAKE01"}`)+`,
			 `+cell(`{"type":"link","url":"https://example.com/p","text":"412 ms"}`)+`],
			[`+cell(text("a | b"))+`,`+cell("")+`,`+cell("")+`],
			[`+cell(text("x"))+`,`+cell(text("y"))+`,`+cell(text("z"))+`]
		]
	}`)

	// An unaligned table omits column_settings, as Slack's own conversion does.
	plain := TextToBlocks("a | b\n--- | ---\n1 | 2", RichTextOptions{})
	if tb, ok := plain[0].(TableBlock); !ok || tb.ColumnSettings != nil || len(tb.Rows) != 2 {
		t.Errorf("outer-pipe-less table = %+v", plain[0])
	}
}

func TestNotTables(t *testing.T) {
	cases := []string{
		"use a | b to pipe",                   // no delimiter row
		"a | b\n---",                          // delimiter without a pipe is a divider
		"| a | b |\n|---|---|---|\n| 1 | 2 |", // delimiter width disagrees with the header
	}
	for _, text := range cases {
		for _, b := range TextToBlocks(text, RichTextOptions{IncludeInlineFormatting: true}) {
			if _, ok := b.(TableBlock); ok {
				t.Errorf("%q became a table", text)
			}
		}
	}
}

func TestChecklists(t *testing.T) {
	got := textToRichText(t, "- [ ] todo\n- [x] done\n  - [X] child", RichTextOptions{})
	jsonEqual(t, "checklist", got[0].Elements, `[
		{"type":"rich_text_list","style":"bullet","checklist":true,"elements":[
			{"type":"rich_text_section","checked":false,"elements":[{"type":"text","text":"todo"}]},
			{"type":"rich_text_section","checked":true,"elements":[{"type":"text","text":"done"}]}
		]},
		{"type":"rich_text_list","style":"bullet","indent":1,"checklist":true,"elements":[
			{"type":"rich_text_section","checked":true,"elements":[{"type":"text","text":"child"}]}
		]}
	]`)

	// A checklist item inside a numbered list keeps its number, and the items
	// after it keep counting.
	lists := listElements(t, textToRichText(t, "1. a\n2. [ ] b\n3. c", RichTextOptions{}))
	offsets := []int{0, 1, 2}
	if len(lists) != 3 {
		t.Fatalf("want 3 runs, got %d", len(lists))
	}
	for i, l := range lists {
		if l.Style != "ordered" || l.Offset != offsets[i] || l.Checklist != (i == 1) {
			t.Errorf("run %d = style %s offset %d checklist %v", i, l.Style, l.Offset, l.Checklist)
		}
	}

	// Not boxes: a link, an empty pair of brackets, a box glued to its text.
	for _, text := range []string{"- [link](https://example.com)", "- [] empty", "- [x]glued"} {
		for _, l := range listElements(t, textToRichText(t, text, RichTextOptions{})) {
			if l.Checklist {
				t.Errorf("%q became a checklist", text)
			}
		}
	}
}

// Structure forces blocks in the Slack-mrkdwn dialect too: its plain text
// field draws no headings, dividers or tables.
func TestStructureForcesBlocksInSlackDialect(t *testing.T) {
	for _, text := range []string{"# Title", "---", "| a |\n|---|\n| 1 |", "- [ ] task"} {
		if TextToBlocks(text, RichTextOptions{SlackMarkdown: true}) == nil {
			t.Errorf("%q: no blocks in Slack-mrkdwn dialect", text)
		}
	}
	// And a <url|label> pipe there is the token's, not a cell break.
	blocks := TextToBlocks("| link |\n|---|\n| <https://example.com|docs> |", RichTextOptions{SlackMarkdown: true})
	tb, ok := blocks[0].(TableBlock)
	if !ok || len(tb.Rows[1]) != 1 {
		t.Fatalf("table = %+v", blocks[0])
	}
	jsonEqual(t, "mrkdwn link cell", tb.Rows[1][0].Elements[0].Elements,
		`[{"type":"link","url":"https://example.com","text":"docs"}]`)
}

func TestHeadingFallbackTextDropsMarkers(t *testing.T) {
	if _, fallback := RenderOutbound("## Release 4.2\nshipped", false); fallback != "Release 4.2\nshipped" {
		t.Errorf("fallback = %q", fallback)
	}
}

func TestLinkChipsInsideTableCells(t *testing.T) {
	text := "| link |\n|---|\n| [https://github.com/acme/widgets](https://github.com/acme/widgets) |"
	if chips := linkChips(upgrade(text, false, tWS)); len(chips) != 1 || chips[0].Text != "github.com/acme/widgets" {
		t.Errorf("table-cell chips = %+v", chips)
	}
}

// Every new structure survives Markdown → blocks → (JSON) → Markdown.
func TestStructureRoundTrip(t *testing.T) {
	src := strings.Join([]string{
		"# Release 4.2",
		"",
		"Shipped **today**.",
		"",
		"---",
		"",
		"## Checks",
		"",
		"- [x] tests",
		"- [ ] docs",
		"    - [ ] skill",
		"",
		"### Latency",
		"",
		"| Service | p95 |",
		"| :--- | ---: |",
		"| checkout | 412 |",
	}, "\n")
	raw, err := json.Marshal(TextToBlocks(src, RichTextOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	var blocks []any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	if got := RenderMessageContent(map[string]any{"blocks": blocks}); got != src {
		t.Errorf("round trip:\n got %q\nwant %q", got, src)
	}
}

func TestImageLines(t *testing.T) {
	cases := []struct {
		name string
		line string
		want ImageBlock
	}{
		{"a local path is left for the caller to upload", "![latency chart](./out/chart.png)",
			ImageBlock{Type: "image", AltText: "latency chart", LocalPath: "./out/chart.png"}},
		{"a public URL is fetched by Slack", "![logo](https://example.com/logo.png)",
			ImageBlock{Type: "image", AltText: "logo", ImageURL: "https://example.com/logo.png"}},
		{"a Slack file id is reused as-is", "![swatch](F0ABCDEF12)",
			ImageBlock{Type: "image", AltText: "swatch", SlackFile: &SlackFile{ID: "F0ABCDEF12"}}},
		{"a title becomes the caption", `![chart](F0ABCDEF12 "Q3 latency")`,
			ImageBlock{Type: "image", AltText: "chart", SlackFile: &SlackFile{ID: "F0ABCDEF12"},
				Title: &PlainText{Type: "plain_text", Text: "Q3 latency", Emoji: true}}},
		{"angle brackets allow spaces in a path", "![x](<./my chart.png>)",
			ImageBlock{Type: "image", AltText: "x", LocalPath: "./my chart.png"}},
		{"empty alt text falls back to the file name, as Slack requires one", "![](./out/chart.png)",
			ImageBlock{Type: "image", AltText: "chart.png", LocalPath: "./out/chart.png"}},
	}
	for _, tc := range cases {
		blocks := TextToBlocks(tc.line, RichTextOptions{})
		if len(blocks) != 1 {
			t.Errorf("%s: %d blocks", tc.name, len(blocks))
			continue
		}
		got, ok := blocks[0].(ImageBlock)
		if !ok {
			t.Errorf("%s: %T, want an image", tc.name, blocks[0])
			continue
		}
		jsonEqual(t, tc.name, got, marshalString(t, tc.want))
		if got.LocalPath != tc.want.LocalPath {
			t.Errorf("%s: LocalPath %q, want %q", tc.name, got.LocalPath, tc.want.LocalPath)
		}
	}

	// Only an image alone on its line is a block; mid-sentence or escaped, it's text.
	for _, text := range []string{"see ![x](./a.png) here", `\![x](./a.png)`} {
		for _, b := range TextToBlocks(text, RichTextOptions{IncludeInlineFormatting: true}) {
			if _, ok := b.(ImageBlock); ok {
				t.Errorf("%q became an image block", text)
			}
		}
	}

	if _, fallback := RenderOutbound("Report\n![latency chart](./chart.png)", false); fallback != "Report\nlatency chart" {
		t.Errorf("fallback = %q, want the alt text in place of the image", fallback)
	}
}

func marshalString(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// An image held by Slack reads back as the same `![alt](F… "title")` that
// would send it again.
func TestImageRoundTrip(t *testing.T) {
	src := "Report\n\n" + `![latency chart](F0ABCDEF12 "Q3 latency")`
	raw, _ := json.Marshal(TextToBlocks(src, RichTextOptions{}))
	var blocks []any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	if got := RenderMessageContent(map[string]any{"blocks": blocks}); got != src {
		t.Errorf("round trip:\n got %q\nwant %q", got, src)
	}
	// A stored block carries Slack's files-pri URL too; the id wins.
	stored := map[string]any{"type": "image", "alt_text": "swatch",
		"image_url":  "https://files.slack.com/files-pri/T0FAKE-F0ABCDEF12/swatch.png",
		"slack_file": map[string]any{"id": "F0ABCDEF12"}}
	if got := RenderMessageContent(map[string]any{"blocks": []any{stored}}); got != "![swatch](F0ABCDEF12)" {
		t.Errorf("stored image = %q", got)
	}
}
