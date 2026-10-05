package render

// Work Object rendering: the newer app-card unfurl shape Slack sends for app
// link unfurls (issue trackers etc.). The attachment carries only
// {from_url, id, work_object_entity} — none of the classic fields — so
// without this path the whole message renders empty. Slack documents only
// the write side (chat.unfurl entity payloads); this read-back shape is
// reverse-engineered from live payloads (see design-docs/behavior-reference.md).

import (
	"maps"
	"slices"
	"strings"
)

// renderWorkObject renders a work_object_entity attachment as
// title(+external_url link), subtitle, then any layout fields. Returns ""
// when the attachment carries no work object.
func renderWorkObject(a map[string]any) string {
	woe, ok := asRecord(a["work_object_entity"])
	if !ok {
		return ""
	}
	layout := workObjectLayout(woe)

	var chunk []string
	if link := slackLink(str(woe["external_url"]), TextObjectValue(layout["title"])); link != "" {
		chunk = append(chunk, link)
	}
	if subtitle := TextObjectValue(layout["subtitle"]); subtitle != "" {
		chunk = append(chunk, subtitle)
	}
	chunk = append(chunk, workObjectFields(layout)...)
	return strings.Join(uniqueTexts(chunk), "\n")
}

// workObjectLayout picks the richest layout: expanded (has fields) over
// compact. header_title/hover_subtitle are app-chrome ("View latest details")
// and never rendered.
func workObjectLayout(woe map[string]any) map[string]any {
	layouts, ok := asRecord(woe["layouts"])
	if !ok {
		return nil
	}
	for _, name := range append([]string{"expanded", "compact"}, slices.Sorted(maps.Keys(layouts))...) {
		if layout, ok := asRecord(layouts[name]); ok {
			return layout
		}
	}
	return nil
}

// workObjectFields renders the expanded layout's labelled fields
// ("Status: Done"); each value is a standard rich_text block.
func workObjectFields(layout map[string]any) []string {
	fields, ok := asRecord(layout["fields"])
	if !ok {
		return nil
	}
	var out []string
	for _, elAny := range asSlice(fields["elements"]) {
		el, ok := asRecord(elAny)
		if !ok {
			continue
		}
		value := strings.TrimSpace(richTextBlockToMrkdwn(el["rich_text"]))
		if value == "" {
			continue
		}
		if label := strings.TrimSpace(str(el["label"])); label != "" {
			value = label + ": " + value
		}
		out = append(out, value)
	}
	return out
}
