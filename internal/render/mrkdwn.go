package render

import (
	"regexp"
	"strings"
)

var (
	// mailto: is included because Slack's composer produces it and this CLI
	// sends it; without it an email link reads back as a raw <mailto:…|label>
	// token that no reader can use.
	mrkdwnLabeledLinkRe = regexp.MustCompile(`<((https?://|mailto:)[^>|]+)\|([^>]+)>`)
	mrkdwnBareLinkRe    = regexp.MustCompile(`<((https?://|mailto:)[^>]+)>`)
	mrkdwnChannelRe     = regexp.MustCompile(`<#[A-Z0-9]+\|([^>]+)>`)
	mrkdwnUserLabelRe   = regexp.MustCompile(`<@([A-Z0-9]+)\|([^>]+)>`)
	mrkdwnUserRe        = regexp.MustCompile(`<@([A-Z0-9]+)>`)
	mrkdwnSpecialRe     = regexp.MustCompile(`<!([a-zA-Z]+)>`)

	// Emphasis conversion (Slack single-delimiter mrkdwn → standard Markdown).
	// Italic _x_ and underline __x__ are already valid Markdown, so only bold and
	// strike are rewritten. Code spans, fenced blocks and <…> tokens are masked
	// first so their * and ~ are never touched.
	mrkdwnFenceRe = regexp.MustCompile("(?s)```.*?```")
	mrkdwnCodeRe  = regexp.MustCompile("`[^`\n]+`")
	mrkdwnAngleRe = regexp.MustCompile(`<[^>\n]+>`)
	// Slack does not open a delimiter that follows a word character, so
	// "2*3 and 4*5", "src/*.go", and "a~b" are literal text on screen.
	// Matching without that rule invents emphasis Slack never displayed —
	// across arithmetic, globs, paths, and tilde-bearing identifiers. RE2 has
	// no lookaround, so both boundaries are captured and put back.
	mrkdwnBoldRe   = regexp.MustCompile(`(^|[\s(\[{"'])\*([^*\n]+)\*($|[\s).,!?;:\]}"'])`)
	mrkdwnStrikeRe = regexp.MustCompile(`(^|[\s(\[{"'])~([^~\n]+)~($|[\s).,!?;:\]}"'])`)

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
	// Each replacement consumes its trailing boundary, which is also the
	// leading boundary of any adjacent run ("*a* *b*"), so repeat until the
	// text settles rather than leaving the neighbour unconverted.
	masked = replaceUntilStable(masked, mrkdwnBoldRe, "$1**$2**$3")
	masked = replaceUntilStable(masked, mrkdwnStrikeRe, "$1~~$2~~$3")
	return restore(masked)
}

// replaceUntilStable applies a boundary-consuming replacement repeatedly until
// it stops changing the text. Bounded because each pass either converts a run
// or terminates.
func replaceUntilStable(text string, re *regexp.Regexp, repl string) string {
	for range 8 {
		next := re.ReplaceAllString(text, repl)
		if next == text {
			return text
		}
		text = next
	}
	return text
}
