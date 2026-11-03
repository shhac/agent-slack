// Block-level conversion: lines → preformatted / quote / section elements and
// standalone heading / divider / table blocks, with dispatch to the list
// collector. List nesting lives in richtext_list.go, the standalone structures
// in structure_outbound.go, and the byte-level inline scanner in richtext.go.
package render

import (
	"regexp"
	"strings"
)

var (
	codeFenceRe  = regexp.MustCompile("^```")
	blockquoteRe = regexp.MustCompile(`^> (.*)$`)
)

// RichTextOptions controls TextToBlocks.
type RichTextOptions struct {
	// IncludeInlineFormatting also returns blocks when the text has inline
	// formatting (links, mentions, bold, …) but no structure. Without it only
	// structure (lists, checklists, headings, dividers, tables) forces blocks,
	// and plain text is left to Slack's own mrkdwn handling.
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
// pieces a send/edit needs: blocks (nil when a plain text field suffices) and the message `text` fallback. In Markdown mode the formatting
// lives in the blocks, so the fallback is flattened to plain (no literal
// **markers**); in Slack-mrkdwn mode the text field renders natively. This is
// the one place the dialect→(blocks,text) rule lives, shared by send and edit.
func RenderOutbound(text string, slackMarkdown bool) ([]Block, string) {
	blocks := TextToBlocks(text, RichTextOptions{
		SlackMarkdown:           slackMarkdown,
		IncludeInlineFormatting: !slackMarkdown,
	})
	fallback := text
	if !slackMarkdown {
		fallback = PlainTextFromMarkdown(text)
	}
	return blocks, fallback
}

// BlocksForText converts text to blocks, always returning at least one —
// unlike TextToBlocks, which returns nil when a plain `text` field would do.
// For contexts like drafts that require `blocks` and have no `text` fallback.
func BlocksForText(text string, opts RichTextOptions) []Block {
	opts.IncludeInlineFormatting = true
	if blocks := TextToBlocks(text, opts); len(blocks) > 0 {
		return blocks
	}
	return []Block{newRichTextBlock([]RichTextElement{{
		Type:     "rich_text_section",
		Elements: inlineToAny(inlineParser(opts)(text)),
	}})}
}

// TextToBlocks converts user-authored text to Block Kit blocks when it
// contains structure Slack's plain `text` field would lose: lists and
// checklists, headings, dividers, tables, code fences, blockquotes (and
// optionally inline formatting). Returns nil when plain text suffices.
func TextToBlocks(text string, opts RichTextOptions) []Block {
	lines := strings.Split(text, "\n")
	inline := inlineParser(opts)
	var b blockBuilder
	hasStructure := false
	hasFormatting := false
	idx := 0

	for idx < len(lines) {
		line := lines[idx]
		switch {
		case codeFenceRe.MatchString(line):
			idx = collectCodeBlock(lines, idx, &b.elements)
			hasFormatting = true
		case isDividerLine(line):
			b.addBlock(newDividerBlock())
			hasStructure = true
			idx = skipBlankLines(lines, idx+1)
		case isTableStart(lines, idx):
			var table TableBlock
			table, idx = collectTable(lines, idx, inline)
			b.addBlock(table)
			hasStructure = true
			idx = skipBlankLines(lines, idx)
		case isHeadingLine(line):
			level, content, _ := parseHeadingLine(line)
			addHeading(&b, level, content, inline)
			hasStructure = true
			idx = skipBlankLines(lines, idx+1)
		case blockquoteRe.MatchString(line):
			idx = collectBlockquote(lines, idx, inline, &b.elements)
			hasFormatting = true
		case isListLine(line):
			hasStructure = true
			idx = collectList(lines, idx, inline, &b.elements)
		default:
			var formatted bool
			idx, formatted = collectPlainText(lines, idx, inline, &b.elements)
			if formatted {
				hasFormatting = true
			}
		}
	}

	// Only structure forces blocks unconditionally: the plain text field has no
	// way to draw a list, heading, divider or table in either dialect. Code fences and quotes set
	// hasFormatting, which is ignored when IncludeInlineFormatting is off — that
	// combination only arises in the Slack-mrkdwn dialect, where the plain text
	// field already renders them natively, so falling back to it loses nothing.
	// An escaped marker carries no style, so nothing above marks it as
	// formatted — but the plain text field cannot express it either. Stripping
	// the backslash and falling back sends Slack a bare "*literal*", which its
	// own parser then renders as bold: the escape is honoured here and undone
	// on arrival. Only the rich_text path keeps the characters inert.
	if opts.IncludeInlineFormatting && markdownEscapeRe.MatchString(text) {
		hasFormatting = true
	}

	if !hasStructure && (!opts.IncludeInlineFormatting || !hasFormatting) {
		return nil
	}
	return b.build()
}

// skipBlankLines steps past the blank lines that follow a standalone block. A
// block already stands apart, so carrying them into the next section would
// open it with an empty line.
func skipBlankLines(lines []string, idx int) int {
	for idx < len(lines) && strings.TrimSpace(lines[idx]) == "" {
		idx++
	}
	return idx
}

func isHeadingLine(line string) bool {
	_, _, ok := parseHeadingLine(line)
	return ok
}

// startsStructure reports whether the line at idx opens something other than a
// plain paragraph, which is where a run of plain text has to stop.
func startsStructure(lines []string, idx int) bool {
	line := lines[idx]
	return isListLine(line) || codeFenceRe.MatchString(line) || blockquoteRe.MatchString(line) ||
		isDividerLine(line) || isHeadingLine(line) || isTableStart(lines, idx)
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
		if startsStructure(lines, idx) {
			break
		}
		textLines = append(textLines, lines[idx])
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
