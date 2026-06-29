package render

import (
	"regexp"
	"strings"
)

// InlineStyle marks rich_text text-element formatting. Underline has no native
// Slack mrkdwn syntax; our Markdown dialect represents it as __underline__.
type InlineStyle struct {
	Bold      bool `json:"bold,omitempty"`
	Italic    bool `json:"italic,omitempty"`
	Strike    bool `json:"strike,omitempty"`
	Underline bool `json:"underline,omitempty"`
	Code      bool `json:"code,omitempty"`
}

// InlineElement is one rich_text inline element. Type selects which of the
// other fields are meaningful (text/style, url/text, name, user_id,
// channel_id, usergroup_id, range; message_ts/thread_ts for a message_mention
// "chip"); the rest stay empty and are omitted from JSON, so the marshalled
// shape matches what Slack's API expects.
type InlineElement struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Style       *InlineStyle `json:"style,omitempty"`
	URL         string       `json:"url,omitempty"`
	Name        string       `json:"name,omitempty"`
	UserID      string       `json:"user_id,omitempty"`
	ChannelID   string       `json:"channel_id,omitempty"`
	MessageTS   string       `json:"message_ts,omitempty"` // message_mention
	ThreadTS    string       `json:"thread_ts,omitempty"`  // message_mention
	UsergroupID string       `json:"usergroup_id,omitempty"`
	Range       string       `json:"range,omitempty"`
}

// messageMentionEl renders a same-workspace message permalink as Slack's inline
// "chip" reference (a message_mention rich_text element) instead of a plain link
// with a below-card unfurl. The fields are all derivable from the permalink.
func messageMentionEl(channelID, messageTS, threadTS, url string) InlineElement {
	return InlineElement{
		Type:      "message_mention",
		ChannelID: channelID,
		MessageTS: messageTS,
		ThreadTS:  threadTS,
		URL:       url,
	}
}

// RichTextElement is a section-level rich_text element. Elements holds
// InlineElement values for sections/preformatted/quotes, and nested
// RichTextElement sections for lists.
type RichTextElement struct {
	Type     string `json:"type"`
	Style    string `json:"style,omitempty"` // rich_text_list: "bullet" | "ordered"
	Indent   int    `json:"indent,omitempty"`
	Elements []any  `json:"elements"`
}

// RichTextBlock is a top-level rich_text block for chat.postMessage.
type RichTextBlock struct {
	Type     string            `json:"type"`
	Elements []RichTextElement `json:"elements"`
}

func textEl(text string) InlineElement { return InlineElement{Type: "text", Text: text} }

// styleElement folds an emphasis style (bold/italic/strike from an enclosing
// *…*/_…_/~…~ span) into an element's existing style, so nested emphasis and
// emphasized links/mentions carry the combined formatting.
func styleElement(el InlineElement, add InlineStyle) InlineElement {
	existing := InlineStyle{}
	if el.Style != nil {
		existing = *el.Style
	}
	if s := mergeStyle(existing, add); s != (InlineStyle{}) {
		el.Style = &s
	}
	return el
}

func styledTextEl(text string, style InlineStyle) InlineElement {
	return InlineElement{Type: "text", Text: text, Style: &style}
}

// ParseInlineElements parses mrkdwn inline formatting into rich_text inline
// elements: *bold*, _italic_, ~strike~, `code`, :emoji:, <url|label>, <url>,
// mention tokens, and bare @U…/@here mentions.
//
// The TS original used one alternation regex with lookarounds, which RE2
// doesn't support; this is the same grammar as a left-to-right scanner
// (at each position the token alternatives are tried in the TS order).
func ParseInlineElements(text string) []InlineElement {
	var elements []InlineElement
	var plain strings.Builder

	flush := func() {
		if plain.Len() > 0 {
			elements = append(elements, textEl(plain.String()))
			plain.Reset()
		}
	}
	emit := func(el InlineElement) {
		flush()
		elements = append(elements, el)
	}
	// emitEmphasis recurses into a *…*/_…_/~…~ span so links, mentions, emoji
	// and nested emphasis inside it become real elements (not literal text),
	// merging the span's style onto each. Code spans never recurse (literal).
	emitEmphasis := func(content string, add InlineStyle) {
		for _, el := range ParseInlineElements(content) {
			emit(styleElement(el, add))
		}
	}

	i := 0
	for i < len(text) {
		switch text[i] {
		case '`':
			if content, end, ok := scanDelimited(text, i, '`'); ok {
				emit(styledTextEl(content, InlineStyle{Code: true}))
				i = end
				continue
			}
		case ':':
			if name, end, ok := scanEmojiShortcode(text, i); ok && boundaryBefore(text, i) {
				emit(InlineElement{Type: "emoji", Name: name})
				i = end
				continue
			}
		case '*':
			if content, end, ok := scanDelimited(text, i, '*'); ok {
				emitEmphasis(content, InlineStyle{Bold: true})
				i = end
				continue
			}
		case '_':
			if content, end, ok := scanDelimited(text, i, '_'); ok {
				emitEmphasis(content, InlineStyle{Italic: true})
				i = end
				continue
			}
		case '~':
			if content, end, ok := scanDelimited(text, i, '~'); ok {
				emitEmphasis(content, InlineStyle{Strike: true})
				i = end
				continue
			}
		case '<':
			if el, end, ok := scanAngleToken(text, i); ok {
				emit(el)
				i = end
				continue
			}
		case '@':
			if el, end, ok := scanBareMention(text, i); ok && boundaryBefore(text, i) {
				emit(el)
				i = end
				continue
			}
		}
		plain.WriteByte(text[i])
		i++
	}
	flush()

	if len(elements) == 0 {
		return []InlineElement{textEl(text)}
	}
	return elements
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// boundaryBefore mirrors the TS `(?:^|(?<=[^A-Za-z0-9_]))` lookbehind: bare
// mentions and emoji must not directly follow a word character.
func boundaryBefore(text string, i int) bool {
	return i == 0 || !isWordByte(text[i-1])
}

func isEmojiNameByte(b byte) bool {
	return isWordByte(b) || b == '+' || b == '-'
}

// scanDelimited matches `x`, *x*, _x_, ~x~: a non-empty run of anything but
// the delimiter, closed by the nearest delimiter.
func scanDelimited(text string, i int, delim byte) (content string, end int, ok bool) {
	rel := strings.IndexByte(text[i+1:], delim)
	if rel < 1 { // -1: unclosed; 0: empty content
		return "", 0, false
	}
	return text[i+1 : i+1+rel], i + 1 + rel + 1, true
}

// scanEmojiShortcode matches :name: where name is [A-Za-z0-9_+-]+ and the
// closing colon is not followed by another name character (so "12:30:00"
// never becomes an emoji).
func scanEmojiShortcode(text string, i int) (name string, end int, ok bool) {
	j := i + 1
	for j < len(text) && isEmojiNameByte(text[j]) {
		j++
	}
	if j == i+1 || j >= len(text) || text[j] != ':' {
		return "", 0, false
	}
	if j+1 < len(text) && isEmojiNameByte(text[j+1]) {
		return "", 0, false
	}
	return text[i+1 : j], j + 1, true
}

var (
	angleUserRe      = regexp.MustCompile(`^@([UWB][A-Z0-9]+)(\|[^>]*)?$`)
	angleChannelRe   = regexp.MustCompile(`^#([CG][A-Z0-9]+)(\|[^>]*)?$`)
	angleUsergroupRe = regexp.MustCompile(`^!subteam\^([A-Z0-9]+)(\|[^>]*)?$`)
	angleBroadcastRe = regexp.MustCompile(`^!(here|channel|everyone)(\|[^>]*)?$`)
	manualLinkRe     = regexp.MustCompile(`^(?i:https?://|mailto:)`)
)

// scanAngleToken classifies a <…> token: mention tokens, then <url|label>,
// then <url>. Non-link angle text like <fix> stays a literal text element so
// round-tripping through Slack doesn't invent links.
func scanAngleToken(text string, i int) (el InlineElement, end int, ok bool) {
	rel := strings.IndexByte(text[i+1:], '>')
	if rel < 0 {
		return InlineElement{}, 0, false
	}
	content := text[i+1 : i+1+rel]
	end = i + 1 + rel + 1

	if m := angleUserRe.FindStringSubmatch(content); m != nil {
		return InlineElement{Type: "user", UserID: m[1]}, end, true
	}
	if m := angleChannelRe.FindStringSubmatch(content); m != nil {
		return InlineElement{Type: "channel", ChannelID: m[1]}, end, true
	}
	if m := angleUsergroupRe.FindStringSubmatch(content); m != nil {
		return InlineElement{Type: "usergroup", UsergroupID: m[1]}, end, true
	}
	if m := angleBroadcastRe.FindStringSubmatch(content); m != nil {
		return InlineElement{Type: "broadcast", Range: m[1]}, end, true
	}

	if url, label, found := strings.Cut(content, "|"); found {
		if url == "" || label == "" {
			return InlineElement{}, 0, false
		}
		if manualLinkRe.MatchString(url) {
			return InlineElement{Type: "link", URL: url, Text: label}, end, true
		}
		return textEl("<" + content + ">"), end, true
	}
	if content == "" {
		return InlineElement{}, 0, false
	}
	if manualLinkRe.MatchString(content) {
		return InlineElement{Type: "link", URL: content}, end, true
	}
	return textEl("<" + content + ">"), end, true
}

// scanBareMention promotes @U05BRPTKL6A and @here/@channel/@everyone written
// without Slack's angle-bracket syntax.
func scanBareMention(text string, i int) (el InlineElement, end int, ok bool) {
	rest := text[i+1:]
	if len(rest) > 0 && (rest[0] == 'U' || rest[0] == 'W' || rest[0] == 'B') {
		j := 1
		for j < len(rest) && isIDByte(rest[j]) {
			j++
		}
		// The TS pattern was @[UWB][A-Z0-9]{6,}\b — at least 6 ID chars after
		// the prefix letter, and the run must end at a word boundary.
		if j >= 7 && (j == len(rest) || !isWordByte(rest[j])) {
			return InlineElement{Type: "user", UserID: rest[:j]}, i + 1 + j, true
		}
	}
	for _, name := range []string{"here", "channel", "everyone"} {
		if !strings.HasPrefix(rest, name) {
			continue
		}
		if len(rest) == len(name) || !isWordByte(rest[len(name)]) {
			return InlineElement{Type: "broadcast", Range: name}, i + 1 + len(name), true
		}
	}
	return InlineElement{}, 0, false
}

func isIDByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z')
}
