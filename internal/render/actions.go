package render

import "strings"

// CompactAction is one interactive Block Kit element a message offers — a
// button, menu, or picker an app is listening on. Rendered content keeps only
// link buttons, so without this an agent cannot tell a card is actionable.
type CompactAction struct {
	BlockID  string `json:"block_id"`
	ActionID string `json:"action_id"`
	Type     string `json:"type"`
	// Text is a button's label or a menu/picker's placeholder.
	Text  string `json:"text,omitempty"`
	Style string `json:"style,omitempty"`
	URL   string `json:"url,omitempty"`
	// Options are the choice labels of a menu, overflow, radio or checkbox
	// group — for an overflow menu they are the actions themselves.
	Options []string `json:"options,omitempty"`
	// OptionCount stands in for Options where they are omitted (list
	// output); 'message get' lists them.
	OptionCount int `json:"option_count,omitempty"`
	// Confirm is the warning Slack shows before dispatching the press. A
	// programmatic press skips that dialog, so the warning travels with the
	// element instead of being lost.
	Confirm string `json:"confirm,omitempty"`
}

var interactiveElementTypes = map[string]bool{
	"button":                     true,
	"overflow":                   true,
	"static_select":              true,
	"external_select":            true,
	"users_select":               true,
	"conversations_select":       true,
	"channels_select":            true,
	"multi_static_select":        true,
	"multi_external_select":      true,
	"multi_users_select":         true,
	"multi_conversations_select": true,
	"multi_channels_select":      true,
	"datepicker":                 true,
	"timepicker":                 true,
	"datetimepicker":             true,
	"radio_buttons":              true,
	"checkboxes":                 true,
}

// InteractiveElement is a raw interactive element paired with the block that
// holds it — the block_id is part of the element's address when pressing it.
type InteractiveElement struct {
	BlockID string
	Element map[string]any
}

// InteractiveElements walks a message's blocks for elements an app handles:
// those in `actions` blocks and a section's accessory. Elements without an
// action_id cannot be addressed and are skipped.
func InteractiveElements(blocks []any) []InteractiveElement {
	var out []InteractiveElement
	add := func(blockID string, v any) {
		el, ok := asRecord(v)
		if !ok || !interactiveElementTypes[str(el["type"])] || str(el["action_id"]) == "" {
			return
		}
		out = append(out, InteractiveElement{BlockID: blockID, Element: el})
	}
	for _, item := range blocks {
		b, ok := asRecord(item)
		if !ok {
			continue
		}
		switch str(b["type"]) {
		case "actions":
			for _, el := range asSlice(b["elements"]) {
				add(str(b["block_id"]), el)
			}
		case "section":
			add(str(b["block_id"]), b["accessory"])
		}
	}
	return out
}

// messageActions shapes a message's interactive elements for output.
func messageActions(blocks []any) []CompactAction {
	elements := InteractiveElements(blocks)
	if len(elements) == 0 {
		return nil
	}
	out := make([]CompactAction, 0, len(elements))
	for _, ie := range elements {
		out = append(out, CompactActionFor(ie))
	}
	return out
}

// compactActions is messageActions with options optionally reduced to a
// count.
func compactActions(blocks []any, withOptions bool) []CompactAction {
	actions := messageActions(blocks)
	if withOptions {
		return actions
	}
	for i := range actions {
		actions[i].OptionCount = len(actions[i].Options)
		actions[i].Options = nil
	}
	return actions
}

// CompactActionFor shapes one interactive element.
func CompactActionFor(ie InteractiveElement) CompactAction {
	el := ie.Element
	return CompactAction{
		BlockID:  ie.BlockID,
		ActionID: str(el["action_id"]),
		Type:     str(el["type"]),
		Text:     ElementLabel(el),
		Style:    str(el["style"]),
		URL:      str(el["url"]),
		Options:  OptionLabels(el),
		Confirm:  confirmText(el["confirm"]),
	}
}

// ElementLabel is a button's text, or a menu/picker's placeholder.
func ElementLabel(el map[string]any) string {
	if text := TextObjectValue(el["text"]); text != "" {
		return text
	}
	return TextObjectValue(el["placeholder"])
}

// ElementOptions returns an element's static choices, flattening option groups.
func ElementOptions(el map[string]any) []map[string]any {
	var out []map[string]any
	collect := func(opts []any) {
		for _, o := range opts {
			if rec, ok := asRecord(o); ok {
				out = append(out, rec)
			}
		}
	}
	collect(asSlice(el["options"]))
	for _, g := range asSlice(el["option_groups"]) {
		if group, ok := asRecord(g); ok {
			collect(asSlice(group["options"]))
		}
	}
	return out
}

// OptionLabels lists an element's choices by label — the same labels a
// --value or --field matches against.
func OptionLabels(el map[string]any) []string {
	var out []string
	for _, o := range ElementOptions(el) {
		if label := TextObjectValue(o["text"]); label != "" {
			out = append(out, label)
		}
	}
	return out
}

func confirmText(v any) string {
	c, ok := asRecord(v)
	if !ok {
		return ""
	}
	parts := make([]string, 0, 2)
	for _, key := range []string{"title", "text"} {
		if s := TextObjectValue(c[key]); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ": ")
}
