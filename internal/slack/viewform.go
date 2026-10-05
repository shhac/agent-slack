package slack

// The pure layer of an app's modal: reading its input blocks and building the
// state that would submit it. No client, no network — table-testable on
// recorded views. The effectful flow lives in appform.go.
//
// Unlike a workflow form there is no schema to consult: the view's own input
// blocks are the form, addressed by label and keyed by block_id (apps reuse
// one action_id across blocks). App forms often arrive pre-filled — an "Edit"
// button opens the current values — so the submission starts from what the
// view already holds and only the named fields change.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// ViewSummary is a modal an app opened, reduced to what a caller needs to
// fill it in: its title and the input fields by label, with their current
// values.
type ViewSummary struct {
	ID     string      `json:"id"`
	Title  string      `json:"title,omitempty"`
	Fields []ViewField `json:"fields,omitempty"`
	// Submitted reports whether the view was filled in and accepted.
	Submitted bool `json:"submitted"`
	// ResponseAction is Slack's answer to a submission ("clear", "update", …).
	ResponseAction string `json:"response_action,omitempty"`
	// Error is why a submission was not made or was rejected.
	Error string `json:"error,omitempty"`
	// Closed reports whether Slack accepted closing the view; false means it
	// may still be open on the user's other clients. A view accepted with
	// "clear" closed itself.
	Closed bool `json:"closed"`
}

// ViewField is one input block of a view.
type ViewField struct {
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Value    string   `json:"value,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// describeView summarises a view's input blocks.
func describeView(view map[string]any) ViewSummary {
	summary := ViewSummary{
		ID:    getStr(view, "id"),
		Title: render.TextObjectValue(view["title"]),
	}
	for _, in := range viewInputs(view) {
		field := ViewField{
			Title:    in.title,
			Type:     getStr(in.element, "type"),
			Required: !in.optional,
			Value:    entryDisplay(in.entry),
		}
		field.Options = render.OptionLabels(in.element)
		summary.Fields = append(summary.Fields, field)
	}
	return summary
}

// viewInput is one input block of a view and its current value.
type viewInput struct {
	blockID  string
	actionID string
	title    string
	element  map[string]any
	optional bool
	entry    map[string]any
}

// viewInputs walks a view's input blocks, seeding each with the view's live
// state (views.get carries it) or else the element's initial value.
func viewInputs(view map[string]any) []viewInput {
	values := getRec(getRec(view, "state"), "values")
	var out []viewInput
	for _, b := range recItems(getArr(view, "blocks")) {
		if getStr(b, "type") != "input" {
			continue
		}
		element := getRec(b, "element")
		in := viewInput{
			blockID:  getStr(b, "block_id"),
			actionID: getStr(element, "action_id"),
			title:    render.TextObjectValue(b["label"]),
			element:  element,
			optional: getBool(b, "optional"),
		}
		if in.blockID == "" || in.actionID == "" {
			continue
		}
		in.entry = getRec(getRec(values, in.blockID), in.actionID)
		if !entryHasValue(in.entry) {
			in.entry = initialEntry(element)
		}
		out = append(out, in)
	}
	return out
}

// initialEntries pairs each initial_* key with the state key its value is
// submitted under. An element carries at most one; the order only makes
// the pick deterministic if a malformed one carries more.
var initialEntries = []struct{ initialKey, stateKey string }{
	{"initial_value", "value"},
	{"initial_option", "selected_option"},
	{"initial_options", "selected_options"},
	{"initial_date", "selected_date"},
	{"initial_time", "selected_time"},
	{"initial_user", "selected_user"},
	{"initial_users", "selected_users"},
	{"initial_conversation", "selected_conversation"},
	{"initial_conversations", "selected_conversations"},
	{"initial_channel", "selected_channel"},
	{"initial_channels", "selected_channels"},
}

// initialEntry turns an element's initial value into the state entry that
// would submit it unchanged, or nil when the element starts empty.
func initialEntry(element map[string]any) map[string]any {
	elemType := getStr(element, "type")
	for _, pair := range initialEntries {
		v := element[pair.initialKey]
		if isEmptyValue(v) {
			continue
		}
		stateKey := pair.stateKey
		if elemType == "rich_text_input" && pair.initialKey == "initial_value" {
			stateKey = "rich_text_value"
		}
		return map[string]any{"type": elemType, stateKey: v}
	}
	return nil
}

// entryHasValue reports whether a state entry holds a value: live state can
// carry an element's key with nothing in it (selected_option: null).
func entryHasValue(entry map[string]any) bool {
	for key, v := range entry {
		if key != "type" && !isEmptyValue(v) {
			return true
		}
	}
	return false
}

// buildViewState lays fields (label → value) over the view's current values
// and returns the views.submit state plus the block_id → label map used to
// label rejections. Every problem is agent-fixable and found before
// anything is sent.
func buildViewState(view map[string]any, fields map[string]string) (map[string]any, map[string]string, error) {
	inputs := viewInputs(view)
	titles := make([]string, 0, len(inputs))
	titlesByBlock := map[string]string{}
	for _, in := range inputs {
		titles = append(titles, in.title)
		titlesByBlock[in.blockID] = in.title
	}

	for _, title := range slices.Sorted(maps.Keys(fields)) {
		in, err := inputByTitle(inputs, title, titles)
		if err != nil {
			return nil, nil, err
		}
		entry, err := formStateEntry(in.element, in.title, fields[title], appFormValues)
		if err != nil {
			return nil, nil, err
		}
		in.entry = entry
	}

	state := map[string]any{}
	for _, in := range inputs {
		if entryHasValue(in.entry) {
			// An input block holds one element, and block_ids are unique.
			state[in.blockID] = map[string]any{in.actionID: in.entry}
			continue
		}
		if !in.optional {
			return nil, nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"required field %q has no value", in.title).
				WithHint("add --field '" + in.title + "=…' — " + nothingSubmittedHint)
		}
	}
	return state, titlesByBlock, nil
}

// inputByTitle finds the one input labelled title (case-insensitively).
func inputByTitle(inputs []viewInput, title string, titles []string) (*viewInput, error) {
	var matched []*viewInput
	for i := range inputs {
		if strings.EqualFold(inputs[i].title, strings.TrimSpace(title)) {
			matched = append(matched, &inputs[i])
		}
	}
	if len(matched) == 0 {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent,
			"the form has no field %q. Fields: %s", title, strings.Join(titles, ", ")).
			WithHint(nothingSubmittedHint)
	}
	if len(matched) > 1 {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent,
			"%d fields in the form are labelled %q, so it cannot be filled by label", len(matched), title).
			WithHint(nothingSubmittedHint + "; use a Slack client for this form")
	}
	return matched[0], nil
}

// entryDisplay renders a state entry's value for a reader: text as-is, an
// option by its label, ids and dates verbatim.
func entryDisplay(entry map[string]any) string {
	if v := getStr(entry, "value"); v != "" {
		return v
	}
	if rt := getRec(entry, "rich_text_value"); rt != nil {
		return strings.TrimSpace(richTextPlain(rt))
	}
	if opt := getRec(entry, "selected_option"); opt != nil {
		return render.TextObjectValue(opt["text"])
	}
	var parts []string
	for _, opt := range recItems(getArr(entry, "selected_options")) {
		parts = append(parts, render.TextObjectValue(opt["text"]))
	}
	for _, key := range []string{"selected_users", "selected_conversations", "selected_channels"} {
		for _, id := range getArr(entry, key) {
			parts = append(parts, fmt.Sprint(id))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	for _, key := range []string{"selected_date", "selected_time", "selected_user", "selected_conversation", "selected_channel"} {
		if v := getStr(entry, key); v != "" {
			return v
		}
	}
	return ""
}

// richTextPlain flattens a rich_text document to its text runs.
func richTextPlain(node map[string]any) string {
	var b strings.Builder
	if text := getStr(node, "text"); text != "" {
		b.WriteString(text)
	}
	for _, child := range recItems(getArr(node, "elements")) {
		b.WriteString(richTextPlain(child))
	}
	if getStr(node, "type") == "rich_text_section" {
		b.WriteString("\n")
	}
	return b.String()
}
