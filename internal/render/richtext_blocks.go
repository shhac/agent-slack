// Block-level conversion: lines → rich_text_list / preformatted / quote /
// section blocks. The byte-level inline scanner lives in richtext.go.
package render

import (
	"regexp"
	"strings"
)

var (
	bulletLineRe  = regexp.MustCompile(`^(\s*)[•◦▪▫▸‣●○◆◇\-*]\s+(.*)$`)
	orderedLineRe = regexp.MustCompile(`^(\s*)\d+[.)]\s+(.*)$`)
	codeFenceRe   = regexp.MustCompile("^```")
	blockquoteRe  = regexp.MustCompile(`^> (.*)$`)
)

// RichTextOptions controls TextToRichTextBlocks.
type RichTextOptions struct {
	// IncludeInlineFormatting also returns blocks when the text has inline
	// formatting (links, mentions, bold, …) but no lists. Without it only
	// list/code/quote structure forces the rich_text path, and plain text is
	// left to Slack's own mrkdwn handling.
	IncludeInlineFormatting bool
}

// TextToRichTextBlocks converts user-authored text to rich_text blocks when
// it contains structure Slack's plain `text` field would lose: bullet or
// numbered lists, code fences, blockquotes (and optionally inline
// formatting). Returns nil when plain text suffices.
func TextToRichTextBlocks(text string, opts RichTextOptions) []RichTextBlock {
	lines := strings.Split(text, "\n")
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
			idx = collectBlockquote(lines, idx, &elements)
			hasFormatting = true
		case bulletLineRe.MatchString(line):
			hasLists = true
			idx = collectList(lines, idx, "bullet", bulletLineRe, &elements)
		case orderedLineRe.MatchString(line):
			hasLists = true
			idx = collectList(lines, idx, "ordered", orderedLineRe, &elements)
		default:
			var formatted bool
			idx, formatted = collectPlainText(lines, idx, &elements)
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

func hasRichInlineFormatting(elements []InlineElement) bool {
	for _, el := range elements {
		if el.Type != "text" || el.Style != nil {
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
func collectBlockquote(lines []string, startIdx int, elements *[]RichTextElement) int {
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
		Elements: inlineToAny(ParseInlineElements(strings.Join(quoteLines, "\n"))),
	})
	return idx
}

// collectPlainText consumes consecutive non-structural lines starting at
// startIdx and, when they aren't all blank, appends a rich_text_section. The
// bool reports whether the run carried rich inline formatting. Returns the
// next index.
func collectPlainText(lines []string, startIdx int, elements *[]RichTextElement) (int, bool) {
	idx := startIdx
	var textLines []string
	for idx < len(lines) {
		l := lines[idx]
		if bulletLineRe.MatchString(l) || orderedLineRe.MatchString(l) ||
			codeFenceRe.MatchString(l) || blockquoteRe.MatchString(l) {
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
	inline := ParseInlineElements(content)
	*elements = append(*elements, RichTextElement{
		Type:     "rich_text_section",
		Elements: inlineToAny(inline),
	})
	return idx, hasRichInlineFormatting(inline)
}

func collectList(lines []string, startIdx int, style string, pattern *regexp.Regexp, elements *[]RichTextElement) int {
	idx := startIdx

	// Base indent comes from the first item; anything ≥ 2 spaces deeper is a
	// sub-item (Slack rich_text_list supports one indent level per list run).
	firstMatch := pattern.FindStringSubmatch(lines[startIdx])
	baseIndent := len(firstMatch[1])

	currentIndent := -1
	var currentItems []any

	flushItems := func() {
		if len(currentItems) == 0 {
			return
		}
		el := RichTextElement{Type: "rich_text_list", Style: style, Elements: currentItems}
		if currentIndent > 0 {
			el.Indent = currentIndent
		}
		*elements = append(*elements, el)
		currentItems = nil
	}

	for idx < len(lines) {
		m := pattern.FindStringSubmatch(lines[idx])
		if m == nil {
			break
		}

		indent := 0
		if len(m[1]) >= baseIndent+2 {
			indent = 1
		}

		if currentIndent != -1 && indent != currentIndent {
			flushItems()
		}
		currentIndent = indent
		currentItems = append(currentItems, RichTextElement{
			Type:     "rich_text_section",
			Elements: inlineToAny(ParseInlineElements(m[2])),
		})
		idx++
	}

	flushItems()
	return idx
}
