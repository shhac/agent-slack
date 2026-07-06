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

	title := workObjectText(layout["title"])
	url := str(woe["external_url"])
	var chunk []string
	switch {
	case title != "" && url != "":
		chunk = append(chunk, "<"+url+"|"+title+">")
	case title != "":
		chunk = append(chunk, title)
	case url != "":
		chunk = append(chunk, url)
	}
	if subtitle := workObjectText(layout["subtitle"]); subtitle != "" {
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
	for _, name := range []string{"expanded", "compact"} {
		if layout, ok := asRecord(layouts[name]); ok {
			return layout
		}
	}
	for _, name := range slices.Sorted(maps.Keys(layouts)) {
		if layout, ok := asRecord(layouts[name]); ok {
			return layout
		}
	}
	return nil
}

// workObjectText unwraps a work-object text object ({"text": …}).
func workObjectText(v any) string {
	t, ok := asRecord(v)
	if !ok {
		return ""
	}
	return strings.TrimSpace(str(t["text"]))
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
