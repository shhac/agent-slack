package render

import "regexp"

// This file owns the final text transforms shared by every rendered message
// body — link rewriting, mention resolution, and inline-emoji images — so the
// conversation (transcript) and digest paths finalize a body identically.

// markdownLinkRe matches the [label](url) form MrkdwnToMarkdown emits, so the
// transcript can rewrite it to the prose form `label (url)`.
var markdownLinkRe = regexp.MustCompile(`\[([^\]]*)\]\((https?://[^)]+)\)`)

// ApplyHyperlinks rewrites [label](url) markdown links via encode (e.g. into OSC
// 8 hyperlinks). With encode nil it is a no-op, so a caller keeps the markdown
// form for the plain/LLM path.
func ApplyHyperlinks(text string, encode func(url, label string) string) string {
	if encode == nil || text == "" {
		return text
	}
	return markdownLinkRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := markdownLinkRe.FindStringSubmatch(m)
		return encode(sub[2], sub[1])
	})
}

// FinalizeContent applies the transcript body's final text transforms, in order:
// links (OSC 8 hyperlinks when active, else plain "label (url)" prose), inline
// entity resolution (r), then inline-emoji images. It is the single place the
// conversation (transcriptContent) and digest (digestBody) paths share, so a
// message body renders identically whichever surface shows it. Empty content
// passes through unchanged.
func FinalizeContent(content string, r MentionResolvers, opts TranscriptOptions) string {
	if content == "" {
		return ""
	}
	if opts.Hyperlink != nil {
		content = ApplyHyperlinks(content, opts.Hyperlink)
	} else {
		content = markdownLinkRe.ReplaceAllString(content, "$1 ($2)")
	}
	content = ResolveMentionsForDisplay(content, r)
	return applyInlineEmoji(content, opts.InlineEmoji)
}

// applyInlineEmoji replaces each custom-emoji shortcode the resolver recognizes
// with its inline-image escape, leaving standard/unknown shortcodes as text. It
// is a no-op when resolve is nil (every machine-output path) and runs last, so
// the escape it inserts is never truncated or rewritten downstream. The resolver
// itself is the filter — only names it returns a non-empty escape for change,
// so `:not_an_emoji:` in prose is left alone.
func applyInlineEmoji(text string, resolve func(name string) string) string {
	if resolve == nil || text == "" {
		return text
	}
	return emojiShortcodeRe.ReplaceAllStringFunc(text, func(m string) string {
		if esc := resolve(m[1 : len(m)-1]); esc != "" {
			return esc
		}
		return m
	})
}
