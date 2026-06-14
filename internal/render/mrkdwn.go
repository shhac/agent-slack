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

	// Emphasis conversion (Slack single-delimiter mrkdwn → standard Markdown).
	// Italic _x_ and underline __x__ are already valid Markdown, so only bold and
	// strike are rewritten. Code spans, fenced blocks and <…> tokens are masked
	// first so their * and ~ are never touched.
	mrkdwnFenceRe  = regexp.MustCompile("(?s)```.*?```")
	mrkdwnCodeRe   = regexp.MustCompile("`[^`\n]+`")
	mrkdwnAngleRe  = regexp.MustCompile(`<[^>\n]+>`)
	mrkdwnBoldRe   = regexp.MustCompile(`\*([^*\n]+)\*`)
	mrkdwnStrikeRe = regexp.MustCompile(`~([^~\n]+)~`)

	mrkdwnEntityReplacer = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")
)

// MrkdwnToMarkdown converts Slack mrkdwn to plain Markdown: emphasis becomes
// standard Markdown (*bold* → **bold**, ~strike~ → ~~strike~~), links become
// [label](url), mention tokens become @name/#name, HTML entities are decoded,
// and :emoji: shortcodes become unicode. With slackMarkdown set, the native
// Slack mrkdwn is returned unchanged (the inbound opt-out).
func MrkdwnToMarkdown(text string, slackMarkdown bool) string {
	if text == "" {
		return ""
	}
	if slackMarkdown {
		return text
	}

	out := convertEmphasisToMarkdown(text)
	out = mrkdwnLabeledLinkRe.ReplaceAllString(out, "[$3]($1)")
	out = mrkdwnBareLinkRe.ReplaceAllString(out, "$1")
	out = mrkdwnChannelRe.ReplaceAllString(out, "#$1")
	out = mrkdwnUserLabelRe.ReplaceAllString(out, "@$2")
	out = mrkdwnUserRe.ReplaceAllString(out, "@$1")
	out = mrkdwnSpecialRe.ReplaceAllString(out, "@$1")
	out = mrkdwnEntityReplacer.Replace(out)
	return EmojifyShortcodes(out)
}

// convertEmphasisToMarkdown rewrites Slack single-delimiter bold/strike to their
// doubled Markdown form, masking code/fence/angle spans so their delimiters are
// preserved verbatim.
func convertEmphasisToMarkdown(text string) string {
	masked, restore := Protect(text, mrkdwnFenceRe, mrkdwnCodeRe, mrkdwnAngleRe)
	masked = mrkdwnBoldRe.ReplaceAllString(masked, "**$1**")
	masked = mrkdwnStrikeRe.ReplaceAllString(masked, "~~$1~~")
	return restore(masked)
}
