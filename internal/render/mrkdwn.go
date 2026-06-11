package render

import (
	"regexp"
	"strings"
)

var (
	mrkdwnLabeledLinkRe = regexp.MustCompile(`<((https?://)[^>|]+)\|([^>]+)>`)
	mrkdwnBareLinkRe    = regexp.MustCompile(`<((https?://)[^>]+)>`)
	mrkdwnChannelRe     = regexp.MustCompile(`<#[A-Z0-9]+\|([^>]+)>`)
	mrkdwnUserLabelRe   = regexp.MustCompile(`<@([A-Z0-9]+)\|([^>]+)>`)
	mrkdwnUserRe        = regexp.MustCompile(`<@([A-Z0-9]+)>`)
	mrkdwnSpecialRe     = regexp.MustCompile(`<!([a-zA-Z]+)>`)

	mrkdwnEntityReplacer = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")
)

// MrkdwnToMarkdown converts Slack mrkdwn to plain Markdown: links become
// [label](url), mention tokens become @name/#name, HTML entities are decoded,
// and :emoji: shortcodes become unicode (smaller for LLM consumption).
func MrkdwnToMarkdown(text string) string {
	if text == "" {
		return ""
	}

	out := mrkdwnLabeledLinkRe.ReplaceAllString(text, "[$3]($1)")
	out = mrkdwnBareLinkRe.ReplaceAllString(out, "$1")
	out = mrkdwnChannelRe.ReplaceAllString(out, "#$1")
	out = mrkdwnUserLabelRe.ReplaceAllString(out, "@$2")
	out = mrkdwnUserRe.ReplaceAllString(out, "@$1")
	out = mrkdwnSpecialRe.ReplaceAllString(out, "@$1")
	out = mrkdwnEntityReplacer.Replace(out)
	return EmojifyShortcodes(out)
}
