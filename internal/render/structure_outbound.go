// Outbound parsing for the line-level structures that become standalone Block
// Kit blocks rather than rich_text elements: ATX headings, thematic breaks and
// GFM pipe tables.
package render

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	// The space after the hashes is what keeps `#general` a channel reference
	// rather than a heading; an optional closing run of #s is dropped.
	headingLineRe = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$`)
	dividerLineRe = regexp.MustCompile(`^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	tableAlignRe  = regexp.MustCompile(`^:?-+:?$`)
	// ![alt](src) or ![alt](src "title") alone on a line; <…> around src
	// allows a path with spaces.
	imageLineRe = regexp.MustCompile(`^ {0,3}!\[([^\]]*)\]\(\s*(<[^>]+>|[^\s)]+)(?:\s+"([^"]*)")?\s*\)[ \t]*$`)
	// slackFileIDRe is an id from a message's files[].id, reusable as-is.
	slackFileIDRe = regexp.MustCompile(`^F[A-Z0-9]{8,}$`)
)

const (
	// maxHeaderLevel is the smallest heading Slack draws: header.level is
	// validated to 1–3, so `####` and deeper become small headings.
	maxHeaderLevel = 3
	// maxHeaderChars is header.text's limit; a longer heading would fail the
	// whole send with invalid_blocks.
	maxHeaderChars = 150
)

func parseHeadingLine(line string) (level int, content string, ok bool) {
	m := headingLineRe.FindStringSubmatch(line)
	if m == nil || strings.TrimSpace(m[2]) == "" {
		return 0, "", false
	}
	return min(len(m[1]), maxHeaderLevel), m[2], true
}

func isDividerLine(line string) bool {
	return dividerLineRe.MatchString(line)
}

// addHeading renders a heading as a header block when it can be one. A header
// holds only plain text, so a heading carrying a mention, channel or link — or
// one past Slack's length limit — becomes a bold line instead: the emphasis
// survives and nothing is silently stripped. Inline styles inside a plain
// heading are dropped, since a header is already set bold.
func addHeading(b *blockBuilder, level int, content string, inline func(string) []InlineElement) {
	elements := inline(content)
	if text, ok := plainHeadingText(elements); ok {
		b.addBlock(newHeaderBlock(level, text))
		return
	}
	b.addElement(boldHeadingSection(elements))
}

func plainHeadingText(elements []InlineElement) (string, bool) {
	var b strings.Builder
	for _, el := range elements {
		switch el.Type {
		case "text":
			b.WriteString(el.Text)
		case "emoji":
			b.WriteString(slackToken("emoji", el.Name))
		default:
			return "", false
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" || utf8.RuneCountInString(text) > maxHeaderChars {
		return "", false
	}
	return text, true
}

// boldHeadingSection is the fallback for a heading a header block cannot hold.
func boldHeadingSection(elements []InlineElement) RichTextElement {
	var out []any
	for _, el := range elements {
		out = append(out, styleElement(el, InlineStyle{Bold: true}))
	}
	return RichTextElement{Type: "rich_text_section", Elements: append(out, textEl("\n"))}
}

// isTableStart reports whether a GFM table begins at idx: a row with a pipe,
// then a delimiter row with the same number of cells. The delimiter row is
// what separates a table from prose that merely contains a `|`.
func isTableStart(lines []string, idx int) bool {
	if idx+1 >= len(lines) || !strings.Contains(lines[idx], "|") {
		return false
	}
	aligns, ok := parseTableDelimiter(lines[idx+1])
	return ok && len(aligns) == len(splitTableRow(lines[idx]))
}

func parseTableDelimiter(line string) ([]string, bool) {
	if !strings.Contains(line, "|") {
		return nil, false
	}
	cells := splitTableRow(line)
	aligns := make([]string, len(cells))
	for i, cell := range cells {
		if !tableAlignRe.MatchString(cell) {
			return nil, false
		}
		left, right := strings.HasPrefix(cell, ":"), strings.HasSuffix(cell, ":")
		switch {
		case left && right:
			aligns[i] = "center"
		case right:
			aligns[i] = "right"
		case left:
			aligns[i] = "left"
		}
	}
	return aligns, true
}

// splitTableRow splits a pipe-table row into trimmed cells. Outer pipes are
// optional, `\|` is a literal pipe inside a cell, and the `|` inside a
// <url|label> token belongs to the token, not the row.
func splitTableRow(line string) []string {
	masked, restore := Protect(strings.TrimSpace(line), mrkdwnAngleRe)
	cells := splitMaskedRow(masked)
	for i, cell := range cells {
		cells[i] = restore(cell)
	}
	return cells
}

func splitMaskedRow(row string) []string {
	row = strings.TrimPrefix(row, "|")
	if strings.HasSuffix(row, "|") && !strings.HasSuffix(row, `\|`) {
		row = row[:len(row)-1]
	}
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(row); i++ {
		switch {
		case row[i] == '\\' && i+1 < len(row) && row[i+1] == '|':
			cell.WriteByte('|')
			i++
		case row[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(row[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// collectTable consumes a table starting at startIdx (isTableStart already
// holds) and returns the table block and the index past it. The table ends at
// the first line without a pipe. Rows are padded or cut to the header's width
// because Slack rejects ragged tables outright.
func collectTable(lines []string, startIdx int, inline func(string) []InlineElement) (TableBlock, int) {
	header := splitTableRow(lines[startIdx])
	aligns, _ := parseTableDelimiter(lines[startIdx+1])
	width := len(header)

	rows := [][]RichTextBlock{tableCells(header, width, inline)}
	idx := startIdx + 2
	for idx < len(lines) && strings.TrimSpace(lines[idx]) != "" && strings.Contains(lines[idx], "|") {
		rows = append(rows, tableCells(splitTableRow(lines[idx]), width, inline))
		idx++
	}
	return TableBlock{Type: "table", ColumnSettings: columnSettings(aligns), Rows: rows}, idx
}

func tableCells(cells []string, width int, inline func(string) []InlineElement) []RichTextBlock {
	out := make([]RichTextBlock, width)
	for i := range out {
		// A raw_text cell must be non-empty, but a rich_text cell with an empty
		// section is accepted — so every cell is rich_text, blank ones included.
		elements := []any{}
		if i < len(cells) && cells[i] != "" {
			elements = inlineToAny(inline(cells[i]))
		}
		out[i] = newRichTextBlock([]RichTextElement{{Type: "rich_text_section", Elements: elements}})
	}
	return out
}

// columnSettings is omitted unless some column is aligned, matching what
// Slack's own Markdown conversion sends for an unaligned table.
func columnSettings(aligns []string) []ColumnSetting {
	aligned := false
	settings := make([]ColumnSetting, len(aligns))
	for i, a := range aligns {
		settings[i] = ColumnSetting{Align: a}
		aligned = aligned || a != ""
	}
	if !aligned {
		return nil
	}
	return settings
}

// parseImageLine reads a Markdown image standing alone on its line. Its source
// is a public URL, a Slack file id, or a local path to upload — which of those
// is decided here, but the upload itself belongs to the caller.
func parseImageLine(line string) (ImageBlock, bool) {
	m := imageLineRe.FindStringSubmatch(line)
	if m == nil {
		return ImageBlock{}, false
	}
	src := strings.TrimSuffix(strings.TrimPrefix(m[2], "<"), ">")
	img := ImageBlock{Type: "image", AltText: strings.TrimSpace(m[1])}
	lower := strings.ToLower(src)
	switch {
	case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
		img.ImageURL = src
	case slackFileIDRe.MatchString(src):
		img.SlackFile = &SlackFile{ID: src}
	default:
		img.LocalPath = src
	}
	if img.AltText == "" {
		// Slack requires alt text; the file's name is the honest default.
		img.AltText = imageNameFallback(src)
	}
	if title := strings.TrimSpace(m[3]); title != "" {
		img.Title = &PlainText{Type: "plain_text", Text: title, Emoji: true}
	}
	return img, true
}

func imageNameFallback(src string) string {
	name := src[strings.LastIndex(src, "/")+1:]
	if name == "" {
		return "image"
	}
	return name
}

func isImageLine(line string) bool {
	_, ok := parseImageLine(line)
	return ok
}
