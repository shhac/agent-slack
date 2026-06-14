package render

import (
	"regexp"
	"strconv"
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
	mrkdwnStashRe  = regexp.MustCompile("\x00(\\d+)\x00")

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
	var stash []string
	mask := func(re *regexp.Regexp, s string) string {
		return re.ReplaceAllStringFunc(s, func(m string) string {
			stash = append(stash, m)
			return "\x00" + strconv.Itoa(len(stash)-1) + "\x00"
		})
	}
	out := mask(mrkdwnFenceRe, text)
	out = mask(mrkdwnCodeRe, out)
	out = mask(mrkdwnAngleRe, out)

	out = mrkdwnBoldRe.ReplaceAllString(out, "**$1**")
	out = mrkdwnStrikeRe.ReplaceAllString(out, "~~$1~~")

	return mrkdwnStashRe.ReplaceAllStringFunc(out, func(m string) string {
		if idx, err := strconv.Atoi(m[1 : len(m)-1]); err == nil && idx < len(stash) {
			return stash[idx]
		}
		return m
	})
}
