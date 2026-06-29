package render

import (
	"regexp"
	"strings"
)

// slackMessageURLRe matches a Slack message permalink anywhere in text:
// https://<sub>.slack.com/archives/<channel>/p<digits>[?query]. Each match is
// validated per-occurrence via ParseMessageURL + SameWorkspaceHost, so a loose
// pattern here is fine.
var slackMessageURLRe = regexp.MustCompile(`https?://[A-Za-z0-9.-]+\.slack\.com/archives/[A-Za-z0-9]+/p\d{7,}(?:\?[A-Za-z0-9_=&.%~-]*)?`)

// UpgradeMessageMentions rewrites same-workspace Slack message permalinks in the
// outbound rich_text blocks into inline "chip" references (message_mention
// elements) — the form Slack's own composer produces when you paste a message
// link, rather than a plain link plus a below-card unfurl.
//
// It handles both shapes: a bare URL inside a text element, and an unlabeled link
// element from <url> / [url](url). An explicitly-labeled link ([label](url) with
// label != url) is left alone — the chip can't carry a label, so a deliberate
// label is preserved. When a permalink is present but the text had no other
// formatting (so RenderOutbound returned no blocks), blocks are force-built so
// the chip can be carried.
func UpgradeMessageMentions(blocks []RichTextBlock, text string, slackMarkdown bool, workspaceURL string) []RichTextBlock {
	if workspaceURL == "" || !containsSameWorkspaceMessageURL(text, workspaceURL) {
		return blocks
	}
	if len(blocks) == 0 {
		blocks = RichTextBlocksForText(text, RichTextOptions{
			SlackMarkdown:           slackMarkdown,
			IncludeInlineFormatting: !slackMarkdown,
		})
	}
	for bi := range blocks {
		for ei := range blocks[bi].Elements {
			blocks[bi].Elements[ei].Elements = upgradeInlineAny(blocks[bi].Elements[ei].Elements, workspaceURL)
		}
	}
	return blocks
}

// containsSameWorkspaceMessageURL reports whether text holds at least one Slack
// message permalink for workspaceURL's host.
func containsSameWorkspaceMessageURL(text, workspaceURL string) bool {
	for _, m := range slackMessageURLRe.FindAllString(text, -1) {
		if _, ok := sameWorkspaceMessageRef(m, workspaceURL); ok {
			return true
		}
	}
	return false
}

// sameWorkspaceMessageRef parses rawURL as a Slack message permalink and returns
// its ref only when it belongs to workspaceURL's host — the single predicate
// behind both the contains-check and the chip construction.
func sameWorkspaceMessageRef(rawURL, workspaceURL string) (*MessageRef, bool) {
	ref, err := ParseMessageURL(rawURL)
	if err != nil || !SameWorkspaceHost(ref.WorkspaceURL, workspaceURL) {
		return nil, false
	}
	return ref, true
}

// upgradeInlineAny walks one section's inline elements, converting message
// permalinks to message_mention chips and recursing into nested list elements.
func upgradeInlineAny(elements []any, workspaceURL string) []any {
	out := make([]any, 0, len(elements))
	for _, e := range elements {
		switch el := e.(type) {
		case InlineElement:
			out = append(out, upgradeInlineElement(el, workspaceURL)...)
		case RichTextElement:
			el.Elements = upgradeInlineAny(el.Elements, workspaceURL)
			out = append(out, el)
		default:
			out = append(out, e)
		}
	}
	return out
}

// upgradeInlineElement returns the replacement element(s) for one inline element:
// an unlabeled same-workspace message link becomes a chip; a text element has its
// bare permalinks split out into chips; everything else is returned unchanged.
func upgradeInlineElement(el InlineElement, workspaceURL string) []any {
	switch el.Type {
	case "link":
		if el.Text == "" { // labeled links keep their label
			if mm, ok := messageMentionFor(el.URL, workspaceURL); ok {
				return []any{mm}
			}
		}
		return []any{el}
	case "text":
		return splitTextMentions(el, workspaceURL)
	default:
		return []any{el}
	}
}

// splitTextMentions splits a text element around any bare same-workspace message
// permalinks, replacing each with a message_mention chip and preserving the
// surrounding text's style.
func splitTextMentions(el InlineElement, workspaceURL string) []any {
	locs := slackMessageURLRe.FindAllStringIndex(el.Text, -1)
	if len(locs) == 0 {
		return []any{el}
	}
	var out []any
	last := 0
	for _, loc := range locs {
		start, end := loc[0], trimTrailingPunct(el.Text, loc[1])
		mm, ok := messageMentionFor(el.Text[start:end], workspaceURL)
		if !ok {
			continue // not same-workspace / unparseable — leave it in the text
		}
		if start > last {
			out = append(out, styledText(el.Text[last:start], el.Style))
		}
		out = append(out, mm)
		last = end
	}
	if last == 0 { // nothing was a same-workspace message URL
		return []any{el}
	}
	if last < len(el.Text) {
		out = append(out, styledText(el.Text[last:], el.Style))
	}
	return out
}

// trimTrailingPunct backs end off any sentence punctuation the loose URL regex
// swallowed (e.g. a permalink ending a sentence: "…p123.").
func trimTrailingPunct(text string, end int) int {
	for end > 0 && strings.ContainsRune(".,;:!?)]}>", rune(text[end-1])) {
		end--
	}
	return end
}

// messageMentionFor builds a chip element for url when it is a same-workspace
// Slack message permalink; thread_ts defaults to the message ts for a root link.
func messageMentionFor(url, workspaceURL string) (InlineElement, bool) {
	ref, ok := sameWorkspaceMessageRef(url, workspaceURL)
	if !ok {
		return InlineElement{}, false
	}
	threadTS := ref.ThreadTSHint
	if threadTS == "" {
		threadTS = ref.MessageTS
	}
	return messageMentionEl(ref.ChannelID, ref.MessageTS, threadTS, url), true
}

func styledText(text string, style *InlineStyle) InlineElement {
	return InlineElement{Type: "text", Text: text, Style: style}
}
