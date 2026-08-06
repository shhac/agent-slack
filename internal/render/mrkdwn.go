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

	// Code spans and fences are literal: nothing inside them is Slack syntax.
	// Masking them for the WHOLE pipeline — not just emphasis — is what stops
	// `:rocket:`, <@U…>, and &amp; being rewritten inside code.
	masked, restore := Protect(text, mrkdwnFenceRe, mrkdwnCodeRe)

	out := convertEmphasisToMarkdown(masked)
	out = mrkdwnLabeledLinkRe.ReplaceAllString(out, "[$3]($1)")
	out = mrkdwnBareLinkRe.ReplaceAllString(out, "$1")
	out = mrkdwnChannelRe.ReplaceAllString(out, "#$1")
	out = mrkdwnUserLabelRe.ReplaceAllString(out, "@$2")
	out = mrkdwnUserRe.ReplaceAllString(out, "@$1")
	out = mrkdwnSpecialRe.ReplaceAllString(out, "@$1")
	out = mrkdwnEntityReplacer.Replace(out)
	return restore(EmojifyShortcodes(out))
}

// convertEmphasisToMarkdown rewrites Slack single-delimiter bold/strike to their
// doubled Markdown form, masking angle spans so a `*` inside <url|label> is not
// read as emphasis. Its caller has already masked code and fences — nesting a
// second Protect over those would let this restore resolve the caller's
// placeholders against the wrong stash.
func convertEmphasisToMarkdown(text string) string {
	masked, restore := Protect(text, mrkdwnAngleRe)
	masked = mrkdwnBoldRe.ReplaceAllString(masked, "**$1**")
	masked = mrkdwnStrikeRe.ReplaceAllString(masked, "~~$1~~")
	return restore(masked)
}
