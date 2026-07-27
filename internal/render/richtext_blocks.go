// Block-level conversion: lines → rich_text_list / preformatted / quote /
// section blocks. The byte-level inline scanner lives in richtext.go.
package render

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	bulletLineRe  = regexp.MustCompile(`^(\s*)[•◦▪▫▸‣●○◆◇\-*]\s+(.*)$`)
	orderedLineRe = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
	codeFenceRe   = regexp.MustCompile("^```")
	blockquoteRe  = regexp.MustCompile(`^> (.*)$`)
)

// tabStop is the column width a tab advances to when measuring list indent, so
// tab- and space-indented sources nest identically.
const tabStop = 4

// maxListIndent bounds the emitted indent level. Slack documents no ceiling and
// stores what it is given, but real messages never nest this far; the cap keeps
// pathological input from putting an unbounded integer on the wire.
const maxListIndent = 8

// RichTextOptions controls TextToRichTextBlocks.
type RichTextOptions struct {
	// IncludeInlineFormatting also returns blocks when the text has inline
	// formatting (links, mentions, bold, …) but no lists. Without it only
	// list/code/quote structure forces the rich_text path, and plain text is
	// left to Slack's own mrkdwn handling.
	IncludeInlineFormatting bool
	// SlackMarkdown interprets the text as Slack mrkdwn (*bold*, _italic_,
	// ~strike~, <url|label>) instead of the default standard Markdown
	// (**bold**, _italic_, ~~strike~~, [label](url), __underline__).
	SlackMarkdown bool
}

// inlineParser picks the inline scanner for the dialect.
func inlineParser(opts RichTextOptions) func(string) []InlineElement {
	if opts.SlackMarkdown {
		return ParseInlineElements
	}
	return ParseMarkdownInline
}

// RenderOutbound converts user text in the given dialect into the outbound
// pieces a send/edit needs: rich_text blocks (nil when a plain text field
// suffices) and the message `text` fallback. In Markdown mode the formatting
// lives in the blocks, so the fallback is flattened to plain (no literal
// **markers**); in Slack-mrkdwn mode the text field renders natively. This is
// the one place the dialect→(blocks,text) rule lives, shared by send and edit.
func RenderOutbound(text string, slackMarkdown bool) ([]RichTextBlock, string) {
	blocks := TextToRichTextBlocks(text, RichTextOptions{
		SlackMarkdown:           slackMarkdown,
		IncludeInlineFormatting: !slackMarkdown,
	})
	fallback := text
	if !slackMarkdown {
		fallback = PlainTextFromMarkdown(text)
	}
	return blocks, fallback
}

// RichTextBlocksForText converts text to rich_text blocks, always returning at
// least one block — unlike TextToRichTextBlocks, which returns nil when a plain
// `text` field would do. For contexts like drafts that require `blocks` and
// have no `text` fallback.
func RichTextBlocksForText(text string, opts RichTextOptions) []RichTextBlock {
	opts.IncludeInlineFormatting = true
	if blocks := TextToRichTextBlocks(text, opts); len(blocks) > 0 {
		return blocks
	}
	return []RichTextBlock{{
		Type: "rich_text",
		Elements: []RichTextElement{{
			Type:     "rich_text_section",
			Elements: inlineToAny(inlineParser(opts)(text)),
		}},
	}}
}

// TextToRichTextBlocks converts user-authored text to rich_text blocks when
// it contains structure Slack's plain `text` field would lose: bullet or
// numbered lists, code fences, blockquotes (and optionally inline
// formatting). Returns nil when plain text suffices.
func TextToRichTextBlocks(text string, opts RichTextOptions) []RichTextBlock {
	lines := strings.Split(text, "\n")
	inline := inlineParser(opts)
	var elements []RichTextElement
	hasLists := false
	hasFormatting := false
	idx := 0

	for idx < len(lines) {
		line := lines[idx]
		switch {
		case codeFenceRe.MatchString(line):
			idx = collectCodeBlock(lines, idx, &elements)
			hasFormatting = true
		case blockquoteRe.MatchString(line):
			idx = collectBlockquote(lines, idx, inline, &elements)
			hasFormatting = true
		case isListLine(line):
			hasLists = true
			idx = collectList(lines, idx, inline, &elements)
		default:
			var formatted bool
			idx, formatted = collectPlainText(lines, idx, inline, &elements)
			if formatted {
				hasFormatting = true
			}
		}
	}

	if !hasLists && (!opts.IncludeInlineFormatting || !hasFormatting) {
		return nil
	}
	return []RichTextBlock{{Type: "rich_text", Elements: elements}}
}

// hasRichInlineFormatting reports whether elements carry styling or a link —
// formatting the plain `text` field can't reproduce, so the rich_text path is
// required. Mentions, channels, broadcasts and emoji render fine in the text
// field, so they do not force blocks on their own.
func hasRichInlineFormatting(elements []InlineElement) bool {
	for _, el := range elements {
		if el.Type == "link" {
			return true
		}
		if el.Type == "text" && el.Style != nil {
			return true
		}
	}
	return false
}

func inlineToAny(elements []InlineElement) []any {
	out := make([]any, len(elements))
	for i, el := range elements {
		out[i] = el
	}
	return out
}

// collectCodeBlock consumes a ``` fenced block starting at startIdx and appends
// a rich_text_preformatted element. Returns the index past the closing fence.
func collectCodeBlock(lines []string, startIdx int, elements *[]RichTextElement) int {
	idx := startIdx + 1 // skip opening ```
	var codeLines []string
	for idx < len(lines) && !codeFenceRe.MatchString(lines[idx]) {
		codeLines = append(codeLines, lines[idx])
		idx++
	}
	if idx < len(lines) {
		idx++ // skip closing ```
	}
	*elements = append(*elements, RichTextElement{
		Type:     "rich_text_preformatted",
		Elements: []any{textEl(strings.Join(codeLines, "\n"))},
	})
	return idx
}

// collectBlockquote consumes consecutive "> " lines starting at startIdx and
// appends a rich_text_quote element. Returns the index past the quote.
func collectBlockquote(lines []string, startIdx int, inline func(string) []InlineElement, elements *[]RichTextElement) int {
	idx := startIdx
	var quoteLines []string
	for idx < len(lines) {
		qm := blockquoteRe.FindStringSubmatch(lines[idx])
		if qm == nil {
			break
		}
		quoteLines = append(quoteLines, qm[1])
		idx++
	}
	*elements = append(*elements, RichTextElement{
		Type:     "rich_text_quote",
		Elements: inlineToAny(inline(strings.Join(quoteLines, "\n"))),
	})
	return idx
}

// collectPlainText consumes consecutive non-structural lines starting at
// startIdx and, when they aren't all blank, appends a rich_text_section. The
// bool reports whether the run carried rich inline formatting. Returns the
// next index.
func collectPlainText(lines []string, startIdx int, inline func(string) []InlineElement, elements *[]RichTextElement) (int, bool) {
	idx := startIdx
	var textLines []string
	for idx < len(lines) {
		l := lines[idx]
		if isListLine(l) || codeFenceRe.MatchString(l) || blockquoteRe.MatchString(l) {
			break
		}
		textLines = append(textLines, l)
		idx++
	}
	content := strings.Join(textLines, "\n")
	if strings.TrimSpace(content) == "" {
		return idx, false
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	parsed := inline(content)
	*elements = append(*elements, RichTextElement{
		Type:     "rich_text_section",
		Elements: inlineToAny(parsed),
	})
	return idx, hasRichInlineFormatting(parsed)
}

// listLine is one parsed source list item.
type listLine struct {
	width int    // leading indent in columns, tabs expanded
	style string // "bullet" | "ordered"
	start int    // the literal number written on an ordered line
	text  string
}

// parseListLine classifies a line as a list item. Bullets are tried first so a
// "- " marker wins over any digits that follow it.
func parseListLine(line string) (listLine, bool) {
	if m := bulletLineRe.FindStringSubmatch(line); m != nil {
		return listLine{width: indentWidth(m[1]), style: "bullet", start: 1, text: m[2]}, true
	}
	if m := orderedLineRe.FindStringSubmatch(line); m != nil {
		start, err := strconv.Atoi(m[2])
		if err != nil {
			start = 1 // a number too long for an int; treat it as an ordinary list
		}
		return listLine{width: indentWidth(m[1]), style: "ordered", start: start, text: m[3]}, true
	}
	return listLine{}, false
}

func isListLine(line string) bool {
	_, ok := parseListLine(line)
	return ok
}

// indentWidth measures leading whitespace in columns.
func indentWidth(s string) int {
	width := 0
	for _, r := range s {
		if r == '\t' {
			width += tabStop - width%tabStop
			continue
		}
		width++
	}
	return width
}

// resumesList reports whether a list continues after the blank lines at idx. A
// blank line between items makes one loose list, not two lists, so numbering
// has to survive it.
func resumesList(lines []string, idx int) bool {
	for idx < len(lines) && strings.TrimSpace(lines[idx]) == "" {
		idx++
	}
	return idx < len(lines) && isListLine(lines[idx])
}

// listRun is a maximal stretch of items sharing one depth and style — the unit
// that becomes a single rich_text_list element.
type listRun struct {
	depth int
	style string
	start int // first item's literal number, seeding a fresh ordered list
	items []any
}

// depthLadder maps indent widths to nesting depths. Widths are only ever
// compared to the ones already opened, so 2-space, 4-space and tab conventions
// all nest, and an outdent to a width nobody opened lands on the nearest
// enclosing level rather than inventing one.
type depthLadder []int

func (l *depthLadder) depthFor(width int) int {
	w := *l
	if len(w) == 0 || width > w[len(w)-1] {
		*l = append(w, width)
		return len(w)
	}
	for len(w) > 1 && width < w[len(w)-1] {
		w = w[:len(w)-1]
	}
	*l = w
	return len(w) - 1
}

// collectList consumes a run of consecutive list lines — bullets and numbers
// mixed, at any nesting depth — and appends one rich_text_list element per
// (depth, style) run. Slack has no nested-list container: depth rides on
// `indent`, and a numbered run interrupted by a sub-list resumes via `offset`.
// Both therefore have to be tracked ACROSS run boundaries, which is why one
// call handles both styles instead of one call per contiguous same-style run.
func collectList(lines []string, startIdx int, inline func(string) []InlineElement, elements *[]RichTextElement) int {
	var ladder depthLadder
	nextNumber := map[int]int{} // depth → offset the next ordered run resumes at
	var run *listRun

	flush := func() {
		if run == nil {
			return
		}
		el := RichTextElement{
			Type:     "rich_text_list",
			Style:    run.style,
			Indent:   min(run.depth, maxListIndent),
			Elements: run.items,
		}
		if run.style == "ordered" {
			offset, resumed := nextNumber[run.depth]
			if !resumed {
				offset = run.start - 1
			}
			el.Offset = offset
			nextNumber[run.depth] = offset + len(run.items)
		}
		*elements = append(*elements, el)
		run = nil
	}

	idx := startIdx
	for idx < len(lines) {
		item, ok := parseListLine(lines[idx])
		if !ok {
			if strings.TrimSpace(lines[idx]) == "" && resumesList(lines, idx+1) {
				idx++
				continue
			}
			break
		}

		depth := ladder.depthFor(item.width)
		if run != nil && (run.depth != depth || run.style != item.style) {
			flush()
		}
		// Returning to a shallower depth closes every list below it, so the next
		// sub-list starts at 1 again instead of resuming its predecessor.
		for d := range nextNumber {
			if d > depth {
				delete(nextNumber, d)
			}
		}
		if run == nil {
			run = &listRun{depth: depth, style: item.style, start: item.start}
		}
		run.items = append(run.items, RichTextElement{
			Type:     "rich_text_section",
			Elements: inlineToAny(inline(item.text)),
		})
		idx++
	}

	flush()
	return idx
}
