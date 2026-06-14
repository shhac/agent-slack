package render

import "strings"

// Standard-Markdown inline parser — the default outbound dialect. Unlike
// ParseInlineElements (Slack mrkdwn, single delimiters), this understands
// CommonMark-ish emphasis with our extensions:
//
//	**bold**      *italic* / _italic_      ***bold italic***
//	~~strike~~    `code`                   [label](url)
//	__underline__ (our extension; CommonMark would call it bold)
//	\* \_ ...     backslash escapes a literal marker
//
// Single ~, single intraword _, and unclosed runs stay literal (so `~123`,
// `file_name_here`, and a stray `**` never trigger or cascade). Styled spans
// nest: inner runs inherit the outer style. Mentions, channels, usergroups,
// broadcasts, emoji and <…> angle tokens reuse the shared scanners, so the two
// dialects agree on everything that isn't emphasis/links/code.
func ParseMarkdownInline(text string) []InlineElement {
	return parseMarkdownInto(text, InlineStyle{})
}

func parseMarkdownInto(text string, base InlineStyle) []InlineElement {
	var elements []InlineElement
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			elements = append(elements, styledOrPlain(plain.String(), base))
			plain.Reset()
		}
	}
	emit := func(els ...InlineElement) {
		flush()
		elements = append(elements, els...)
	}

	i := 0
	for i < len(text) {
		switch text[i] {
		case '\\':
			if i+1 < len(text) && isMarkdownEscapable(text[i+1]) {
				plain.WriteByte(text[i+1])
				i += 2
				continue
			}
		case '`':
			if content, end, ok := scanDelimited(text, i, '`'); ok {
				st := base
				st.Code = true
				emit(styledTextEl(content, st))
				i = end
				continue
			}
		case ':':
			if name, end, ok := scanEmojiShortcode(text, i); ok && boundaryBefore(text, i) {
				emit(InlineElement{Type: "emoji", Name: name})
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
		case '[':
			if el, end, ok := scanMarkdownLink(text, i); ok {
				emit(el)
				i = end
				continue
			}
		case '*', '_', '~':
			if content, style, end, ok := scanMarkdownEmphasis(text, i); ok {
				emit(parseMarkdownInto(content, mergeStyle(base, style))...)
				i = end
				continue
			}
		}
		plain.WriteByte(text[i])
		i++
	}
	flush()

	if len(elements) == 0 {
		return []InlineElement{styledOrPlain(text, base)}
	}
	return elements
}

// scanMarkdownEmphasis matches an emphasis run (**, *, ***, __, _, ___, ~~) at i
// and its nearest valid closing run, returning the inner content (to be parsed
// recursively) and the style it applies. Underscore runs require a non-word
// boundary on both sides so snake_case identifiers aren't italicised.
func scanMarkdownEmphasis(text string, i int) (content string, style InlineStyle, end int, ok bool) {
	type token struct {
		delim string
		style InlineStyle
	}
	var tokens []token
	switch text[i] {
	case '*':
		tokens = []token{
			{"***", InlineStyle{Bold: true, Italic: true}},
			{"**", InlineStyle{Bold: true}},
			{"*", InlineStyle{Italic: true}},
		}
	case '_':
		if !boundaryBefore(text, i) {
			return "", InlineStyle{}, 0, false
		}
		tokens = []token{
			{"___", InlineStyle{Underline: true, Italic: true}},
			{"__", InlineStyle{Underline: true}},
			{"_", InlineStyle{Italic: true}},
		}
	case '~':
		tokens = []token{{"~~", InlineStyle{Strike: true}}}
	default:
		return "", InlineStyle{}, 0, false
	}

	underscore := text[i] == '_'
	for _, t := range tokens {
		if !strings.HasPrefix(text[i:], t.delim) {
			continue
		}
		n := len(t.delim)
		from := i + n
		for {
			rel := strings.Index(text[from:], t.delim)
			if rel < 0 {
				break
			}
			j := from + rel
			if j == i+n { // empty content
				from = j + n
				continue
			}
			if text[j-1] == '\\' { // escaped delimiter, not a closer
				from = j + n
				continue
			}
			if underscore {
				after := j + n
				if after < len(text) && isWordByte(text[after]) {
					from = j + n
					continue
				}
			}
			return text[i+n : j], t.style, j + n, true
		}
	}
	return "", InlineStyle{}, 0, false
}

// scanMarkdownLink matches [label](url). A bare [text](url) where label == url
// drops the label so it renders like a plain link. The label is kept as plain
// text in v1 (no inline formatting inside link labels).
func scanMarkdownLink(text string, i int) (InlineElement, int, bool) {
	closeBracket := strings.IndexByte(text[i+1:], ']')
	if closeBracket < 0 {
		return InlineElement{}, 0, false
	}
	labelEnd := i + 1 + closeBracket
	if labelEnd+1 >= len(text) || text[labelEnd+1] != '(' {
		return InlineElement{}, 0, false
	}
	closeParen := strings.IndexByte(text[labelEnd+2:], ')')
	if closeParen < 0 {
		return InlineElement{}, 0, false
	}
	urlEnd := labelEnd + 2 + closeParen
	label := text[i+1 : labelEnd]
	url := text[labelEnd+2 : urlEnd]
	if url == "" {
		return InlineElement{}, 0, false
	}
	el := InlineElement{Type: "link", URL: url}
	if label != "" && label != url {
		el.Text = label
	}
	return el, urlEnd + 1, true
}

func styledOrPlain(s string, style InlineStyle) InlineElement {
	if style == (InlineStyle{}) {
		return textEl(s)
	}
	return styledTextEl(s, style)
}

func mergeStyle(a, b InlineStyle) InlineStyle {
	return InlineStyle{
		Bold:      a.Bold || b.Bold,
		Italic:    a.Italic || b.Italic,
		Strike:    a.Strike || b.Strike,
		Underline: a.Underline || b.Underline,
		Code:      a.Code || b.Code,
	}
}

func isMarkdownEscapable(b byte) bool {
	switch b {
	case '\\', '*', '_', '~', '`', '[', ']', '(', ')', '<', '>', '@', ':':
		return true
	}
	return false
}
