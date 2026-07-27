// Block-level conversion: lines → preformatted / quote / section blocks, and
// dispatch to the list collector. List nesting lives in richtext_list.go; the
// byte-level inline scanner lives in richtext.go.
package render

import (
	"regexp"
	"strings"
)

var (
	codeFenceRe  = regexp.MustCompile("^```")
	blockquoteRe = regexp.MustCompile(`^> (.*)$`)
)

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

	// Only lists force blocks unconditionally. Code fences and quotes set
	// hasFormatting, which is ignored when IncludeInlineFormatting is off — that
	// combination only arises in the Slack-mrkdwn dialect, where the plain text
	// field already renders them natively, so falling back to it loses nothing.
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
