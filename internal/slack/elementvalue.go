package slack

// The value layer shared by every path that fills a Block Kit element: a
// workflow form, an app's modal, and a menu or picker pressed on a message.
// Slack wants each value in the shape of the element's own type
// (selected_option, selected_date, selected_user, …) — the same keys in a
// views.submit state entry and in a block_actions action.

import (
	"strings"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

const nothingSubmittedHint = "nothing was submitted"

// formStateEntry builds one element value in the shape the element's type
// expects — views.submit rejects state whose type does not match the
// rendered element, and reports it only via response_action "errors".
// Multi-value elements take comma-separated values.
func formStateEntry(element map[string]any, title, value string) (map[string]any, error) {
	elemType := FirstNonEmpty(getStr(element, "type"), "plain_text_input")
	switch elemType {
	case "plain_text_input", "number_input", "email_text_input", "url_text_input":
		return map[string]any{"type": elemType, "value": value}, nil
	case "rich_text_input":
		return map[string]any{"type": elemType, "rich_text_value": richTextValue(value)}, nil
	case "static_select", "radio_buttons", "overflow":
		opt, err := matchElementOption(element, title, value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": elemType, "selected_option": opt}, nil
	case "checkboxes", "multi_static_select":
		var opts []any
		for _, part := range splitValues(value) {
			opt, err := matchElementOption(element, title, part)
			if err != nil {
				return nil, err
			}
			opts = append(opts, opt)
		}
		return map[string]any{"type": elemType, "selected_options": opts}, nil
	case "datepicker":
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"field %q expects a date, got %q", title, value).
				WithHint("use YYYY-MM-DD and rerun — " + nothingSubmittedHint)
		}
		return map[string]any{"type": elemType, "selected_date": value}, nil
	case "timepicker":
		if _, err := time.Parse("15:04", value); err != nil {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"field %q expects a time, got %q", title, value).
				WithHint("use HH:MM (24h) and rerun — " + nothingSubmittedHint)
		}
		return map[string]any{"type": elemType, "selected_time": value}, nil
	}
	if key, ok := idSelectKeys[elemType]; ok {
		return idSelectEntry(elemType, key, title, value)
	}
	return nil, agenterrors.Newf(agenterrors.FixableByHuman,
		"field %q is a %s input, which agent-slack cannot fill", title, elemType).
		WithHint(nothingSubmittedHint + "; use a Slack client for this one")
}

// idSelectKeys maps the user/channel/conversation menus to the key their
// value travels under. They take ids, not names: their options are not on
// the element, so there is no label to match.
var idSelectKeys = map[string]string{
	"users_select":               "selected_user",
	"conversations_select":       "selected_conversation",
	"channels_select":            "selected_channel",
	"multi_users_select":         "selected_users",
	"multi_conversations_select": "selected_conversations",
	"multi_channels_select":      "selected_channels",
}

func idSelectEntry(elemType, key, title, value string) (map[string]any, error) {
	ids := splitValues(value)
	if len(ids) == 0 {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "field %q needs an id", title).
			WithHint("pass a U… or C… id ('user get' / 'channel get' resolve names) — " + nothingSubmittedHint)
	}
	if !strings.HasPrefix(elemType, "multi_") {
		if len(ids) > 1 {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent, "field %q takes one id, got %d", title, len(ids)).
				WithHint(nothingSubmittedHint)
		}
		return map[string]any{"type": elemType, key: ids[0]}, nil
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	return map[string]any{"type": elemType, key: values}, nil
}

func splitValues(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// richTextValue wraps a plain string in the minimal rich_text document a
// rich_text_input element expects.
func richTextValue(value string) map[string]any {
	return map[string]any{
		"type": "rich_text",
		"elements": []any{map[string]any{
			"type":     "rich_text_section",
			"elements": []any{map[string]any{"type": "text", "text": value}},
		}},
	}
}

// matchElementOption finds the element option whose value or label matches
// (labels case-insensitively) and returns the option object verbatim —
// Slack expects the full option, text object included. Grouped options
// (option_groups) are flattened in.
func matchElementOption(element map[string]any, title, value string) (map[string]any, error) {
	options := recItems(getArr(element, "options"))
	for _, group := range recItems(getArr(element, "option_groups")) {
		options = append(options, recItems(getArr(group, "options"))...)
	}
	var labels []string
	for _, opt := range options {
		label := getStr(getRec(opt, "text"), "text")
		if getStr(opt, "value") == value || strings.EqualFold(label, value) {
			return opt, nil
		}
		labels = append(labels, label)
	}
	return nil, agenterrors.Newf(agenterrors.FixableByAgent,
		"field %q has no option matching %q. Available: %s", title, value, strings.Join(labels, ", ")).
		WithHint("match an option by its label or value and rerun — " + nothingSubmittedHint)
}
