package slack

// Filling in a modal an app opened. Unlike a workflow form there is no schema
// to consult: the view's own input blocks are the form, addressed by label
// and keyed by block_id (apps reuse one action_id across blocks). App forms
// often arrive pre-filled — an "Edit" button opens the current values — so
// the submission starts from what the view already holds and only the named
// fields change.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

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
		if len(in.entry) == 0 {
			in.entry = initialEntry(element)
		}
		out = append(out, in)
	}
	return out
}

// initialEntries maps an element's initial_* key to the state key its value
// is submitted under.
var initialEntries = map[string]string{
	"initial_value":         "value",
	"initial_option":        "selected_option",
	"initial_options":       "selected_options",
	"initial_date":          "selected_date",
	"initial_time":          "selected_time",
	"initial_user":          "selected_user",
	"initial_users":         "selected_users",
	"initial_conversation":  "selected_conversation",
	"initial_conversations": "selected_conversations",
	"initial_channel":       "selected_channel",
	"initial_channels":      "selected_channels",
}

// initialEntry turns an element's initial value into the state entry that
// would submit it unchanged, or nil when the element starts empty.
func initialEntry(element map[string]any) map[string]any {
	elemType := getStr(element, "type")
	for initialKey, stateKey := range initialEntries {
		v, ok := element[initialKey]
		if !ok || v == nil || v == "" {
			continue
		}
		if elemType == "rich_text_input" && initialKey == "initial_value" {
			stateKey = "rich_text_value"
		}
		return map[string]any{"type": elemType, stateKey: v}
	}
	return nil
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
		var matched []int
		for i, in := range inputs {
			if strings.EqualFold(in.title, strings.TrimSpace(title)) {
				matched = append(matched, i)
			}
		}
		switch len(matched) {
		case 0:
			return nil, nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"the form has no field %q. Fields: %s", title, strings.Join(titles, ", ")).
				WithHint(nothingSubmittedHint)
		case 1:
		default:
			return nil, nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"%d fields in the form are labelled %q, so it cannot be filled by label", len(matched), title).
				WithHint(nothingSubmittedHint + "; use a Slack client for this form")
		}
		in := &inputs[matched[0]]
		entry, err := formStateEntry(in.element, in.title, fields[title])
		if err != nil {
			return nil, nil, err
		}
		in.entry = entry
	}

	state := map[string]any{}
	for _, in := range inputs {
		if len(in.entry) == 0 {
			if !in.optional {
				return nil, nil, agenterrors.Newf(agenterrors.FixableByAgent,
					"required field %q has no value", in.title).
					WithHint("add --field '" + in.title + "=…' — " + nothingSubmittedHint)
			}
			continue
		}
		block, _ := state[in.blockID].(map[string]any)
		if block == nil {
			block = map[string]any{}
			state[in.blockID] = block
		}
		block[in.actionID] = in.entry
	}
	return state, titlesByBlock, nil
}

// ViewSubmitResult is Slack's answer to submitting a view.
type ViewSubmitResult struct {
	ResponseAction string
}

// SubmitView fills a view an app opened with fields (label → value) and
// submits it. A field problem or a Slack-side rejection is an error and the
// view is left for the caller to close; an accepted submission whose
// response_action is "update" or "push" has moved the form to another step,
// which is still open.
func SubmitView(ctx context.Context, c *Client, view map[string]any, fields map[string]string) (ViewSubmitResult, error) {
	state, titlesByBlock, err := buildViewState(view, fields)
	if err != nil {
		return ViewSubmitResult{}, err
	}
	stateJSON, _ := json.Marshal(map[string]any{"values": state})
	resp, err := c.API(ctx, "views.submit", map[string]any{
		"view_id":      getStr(view, "id"),
		"client_token": clientToken(),
		"state":        string(stateJSON),
	})
	if err != nil {
		var apiErr *agenterrors.APIError
		if agenterrors.As(err, &apiErr) && apiErr.FixableBy == agenterrors.FixableByRetry {
			return ViewSubmitResult{}, agenterrors.Wrap(err, agenterrors.FixableByAgent).
				WithHint("the form may have been submitted — check the message before pressing again")
		}
		return ViewSubmitResult{}, err
	}
	if detail, rejected := rejectedFields(resp, titlesByBlock); rejected {
		return ViewSubmitResult{}, agenterrors.Newf(agenterrors.FixableByAgent,
			"the app rejected the form: %s", detail).
			WithHint("fix the field values and press again — " + nothingSubmittedHint)
	}
	return ViewSubmitResult{ResponseAction: getStr(resp, "response_action")}, nil
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
