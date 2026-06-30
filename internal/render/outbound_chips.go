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

// UpgradeOutboundLinks rewrites links in the outbound rich_text blocks into the
// inline "chips" Slack's own composer produces, so an agent's message reads like
// a human's:
//
//   - a same-workspace message permalink becomes a message_mention chip (channel
//     + ts), rather than a plain link plus a clunky below-card unfurl;
//   - any other unlabeled web URL ([url](url) or <url>) becomes a link chip — a
//     link whose label is the scheme-stripped URL, the form pasting a bare URL
//     into Slack yields.
//
// A deliberately-labeled link ([label](url) / <url|label>) is always left alone:
// the label is the author's intent. Message permalinks are recognised as bare
// URLs in plain text too; link chips only upgrade the explicit unlabeled forms,
// matching the documented "don't expect a bare URL to autolink" contract.
//
// A single walk both detects and rewrites. When the text had no other formatting
// (so RenderOutbound returned no blocks), blocks are force-built to carry a chip;
// if that build turns up nothing to upgrade, the original (empty) blocks are
// returned so plain text stays block-free.
func UpgradeOutboundLinks(blocks []RichTextBlock, text string, slackMarkdown bool, workspaceURL string) []RichTextBlock {
	forced := len(blocks) == 0
	work := blocks
	if forced {
		work = RichTextBlocksForText(text, RichTextOptions{
			SlackMarkdown:           slackMarkdown,
			IncludeInlineFormatting: !slackMarkdown,
		})
	}
	changed := false
	for bi := range work {
		for ei := range work[bi].Elements {
			upgraded, did := upgradeInlineAny(work[bi].Elements[ei].Elements, workspaceURL)
			work[bi].Elements[ei].Elements = upgraded
			changed = changed || did
		}
	}
	if forced && !changed {
		return blocks // nothing to carry — keep plain text block-free
	}
	return work
}

// sameWorkspaceMessageRef parses rawURL as a Slack message permalink and returns
// its ref only when it belongs to workspaceURL's host — the single predicate
// behind both the message-mention check and the chip construction.
func sameWorkspaceMessageRef(rawURL, workspaceURL string) (*MessageRef, bool) {
	ref, err := ParseMessageURL(rawURL)
	if err != nil || !SameWorkspaceHost(ref.WorkspaceURL, workspaceURL) {
		return nil, false
	}
	return ref, true
}

// upgradeInlineAny walks one section's inline elements, converting links to chips
// and recursing into nested list elements; it reports whether anything changed.
func upgradeInlineAny(elements []any, workspaceURL string) ([]any, bool) {
	out := make([]any, 0, len(elements))
	changed := false
	for _, e := range elements {
		switch el := e.(type) {
		case InlineElement:
			repl, did := upgradeInlineElement(el, workspaceURL)
			out = append(out, repl...)
			changed = changed || did
		case RichTextElement:
			sub, did := upgradeInlineAny(el.Elements, workspaceURL)
			el.Elements = sub
			out = append(out, el)
			changed = changed || did
		default:
			out = append(out, e)
		}
	}
	return out, changed
}

// upgradeInlineElement returns the replacement element(s) for one inline element
// and whether it changed: an unlabeled link becomes a message_mention or link
// chip; a text element has its bare message permalinks split out into chips;
// everything else is unchanged.
func upgradeInlineElement(el InlineElement, workspaceURL string) ([]any, bool) {
	switch el.Type {
	case "link":
		up, did := upgradeLink(el, workspaceURL)
		return []any{up}, did
	case "text":
		return splitTextMentions(el, workspaceURL)
	default:
		return []any{el}, false
	}
}

// upgradeLink upgrades an unlabeled link to its chip form, preferring a
// same-workspace message_mention over a plain link chip; a labeled link (and any
// non-web URL) is returned unchanged.
func upgradeLink(el InlineElement, workspaceURL string) (InlineElement, bool) {
	if el.Text != "" { // labeled links keep their label
		return el, false
	}
	if mm, ok := messageMentionFor(el.URL, workspaceURL); ok {
		return mm, true
	}
	if chip, ok := linkChipFor(el); ok {
		return chip, true
	}
	return el, false
}

// splitTextMentions splits a text element around any bare same-workspace message
// permalinks, replacing each with a message_mention chip and preserving the
// surrounding text's style; it reports whether a split happened. (Bare web URLs
// are left as text — only the explicit unlabeled link forms become link chips.)
func splitTextMentions(el InlineElement, workspaceURL string) ([]any, bool) {
	locs := slackMessageURLRe.FindAllStringIndex(el.Text, -1)
	if len(locs) == 0 {
		return []any{el}, false
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
			out = append(out, styledTextEl(el.Text[last:start], el.Style))
		}
		out = append(out, mm)
		last = end
	}
	if last == 0 { // nothing was a same-workspace message URL
		return []any{el}, false
	}
	if last < len(el.Text) {
		out = append(out, styledTextEl(el.Text[last:], el.Style))
	}
	return out, true
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

// linkChipFor builds a link chip for el when it is an unlabeled web URL; it
// reports false for labeled links and non-web (non-http) URLs.
func linkChipFor(el InlineElement) (InlineElement, bool) {
	if el.Type != "link" || el.Text != "" {
		return InlineElement{}, false
	}
	label, ok := chipLabel(el.URL)
	if !ok {
		return InlineElement{}, false
	}
	return linkChipEl(el.URL, label), true
}

// chipLabel strips the scheme and a trailing slash from an http(s) URL to give
// the shortened label Slack's composer shows on a link chip (https://github.com/x
// → github.com/x). It reports false for any non-web URL or one that wouldn't
// shorten.
func chipLabel(rawURL string) (string, bool) {
	lower := strings.ToLower(rawURL)
	var n int
	switch {
	case strings.HasPrefix(lower, "https://"):
		n = len("https://")
	case strings.HasPrefix(lower, "http://"):
		n = len("http://")
	default:
		return "", false
	}
	label := strings.TrimSuffix(rawURL[n:], "/")
	if label == "" {
		return "", false
	}
	return label, true
}
