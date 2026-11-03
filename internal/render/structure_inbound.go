// Inbound rendering for the structural blocks Slack's composer now writes —
// headings, dividers and tables — as their Markdown equivalents. Like the rest
// of the inbound path these emit mrkdwn-compatible text (Slack tokens intact)
// for MrkdwnToMarkdown's single final pass.
package render

import "strings"

const dividerMarkdown = "---"

// headerMarkdown renders a header block as an ATX heading. A header with no
// level predates the composer's heading styles — the title of an app
// notification — and reads as the top-level heading it visually is.
func headerMarkdown(b map[string]any) string {
	text, ok := mrkdwnTextValue(b["text"])
	// An ATX heading is one line; a stray newline would spill the rest of the
	// title into the paragraph below it.
	text = strings.Join(strings.Fields(text), " ")
	if !ok || text == "" {
		return ""
	}
	return strings.Repeat("#", max(asInt(b["level"]), 1)) + " " + text
}

// tableMarkdown renders a table block as a GFM pipe table, the first row as its
// header. Alignment comes back from column_settings so a table read and re-sent
// keeps its shape.
func tableMarkdown(b map[string]any) string {
	rows := asSlice(b["rows"])
	if len(rows) == 0 {
		return ""
	}
	width := 0
	for _, row := range rows {
		width = max(width, len(asSlice(row)))
	}
	if width == 0 {
		return ""
	}

	settings := asSlice(b["column_settings"])
	delimiters := make([]string, width)
	for i := range delimiters {
		setting, _ := asRecord(settingAt(settings, i))
		delimiters[i] = alignDelimiter(str(setting["align"]))
	}

	lines := []string{tableRow(asSlice(rows[0]), width), tableLine(delimiters)}
	for _, row := range rows[1:] {
		lines = append(lines, tableRow(asSlice(row), width))
	}
	return strings.Join(lines, "\n")
}

func settingAt(settings []any, i int) any {
	if i < len(settings) {
		return settings[i]
	}
	return nil
}

func alignDelimiter(align string) string {
	switch align {
	case "left":
		return ":---"
	case "center":
		return ":---:"
	case "right":
		return "---:"
	}
	return "---"
}

// tableRow renders one row padded to the table's width: Slack rejects ragged
// rows, but a reader should still see every column the widest row has.
func tableRow(cells []any, width int) string {
	out := make([]string, width)
	for i := range out {
		if i < len(cells) {
			out[i] = tableCellMarkdown(cells[i])
		}
	}
	return tableLine(out)
}

func tableLine(cells []string) string {
	return "| " + strings.Join(cells, " | ") + " |"
}

// tableCellMarkdown flattens a cell to one line. A pipe-table row cannot span
// lines, and an unescaped `|` would split the cell — but the `|` inside a
// <url|label> token is Slack syntax the final pass still has to read.
func tableCellMarkdown(cellAny any) string {
	cell, ok := asRecord(cellAny)
	if !ok {
		return ""
	}
	var text string
	switch str(cell["type"]) {
	case "rich_text":
		text = richTextBlockToMrkdwn(cell)
	case "raw_text":
		text = str(cell["text"])
	default:
		text = elementFallbackText(cell)
	}
	text = strings.Join(strings.Fields(text), " ")
	masked, restore := Protect(text, mrkdwnAngleRe)
	return restore(strings.ReplaceAll(masked, "|", `\|`))
}

// trimDividers keeps a divider only where it separates content. Leading,
// trailing and repeated dividers carry nothing, and a message made only of
// dividers must render empty so the caller falls back to its `text`.
func trimDividers(parts []string) []string {
	var out []string
	for _, p := range parts {
		if p == dividerMarkdown && (len(out) == 0 || out[len(out)-1] == dividerMarkdown) {
			continue
		}
		out = append(out, p)
	}
	if len(out) > 0 && out[len(out)-1] == dividerMarkdown {
		out = out[:len(out)-1]
	}
	return out
}
