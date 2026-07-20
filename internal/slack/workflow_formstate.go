// The pure form-state layer of workflow submission: mapping a rendered
// form view plus user-supplied Title=value pairs onto the state shape
// views.submit expects. No client, no network — table-testable on recorded
// view payloads. The effectful submit flow lives in workflow_submit.go
// (mirroring the workflows.go / workflows_schema.go split).
package slack

import (
	"fmt"
	"strings"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// ValidateWorkflowFields checks user-supplied Title=value pairs against the
// schema (case-insensitive on titles) and returns the problems found.
func ValidateWorkflowFields(fields map[string]string, schema WorkflowSchema) []string {
	var errs []string
	titles := make([]string, 0, len(schema.Fields))
	known := map[string]bool{}
	for _, f := range schema.Fields {
		titles = append(titles, f.Title)
		known[strings.ToLower(f.Title)] = true
	}
	for title := range fields {
		if !known[strings.ToLower(title)] {
			errs = append(errs, fmt.Sprintf("unknown field %q. Available: %s", title, strings.Join(titles, ", ")))
		}
	}
	for _, f := range schema.Fields {
		if f.Required && !hasField(fields, f.Title) {
			errs = append(errs, fmt.Sprintf("required field %q is missing", f.Title))
		}
	}
	return errs
}

// buildFormState maps the user-supplied values onto the opened view's blocks
// — the shape views.submit expects, mirroring each element's own input type.
// A supplied field that maps to no block errors rather than silently
// shrinking the submission (a stub view would otherwise submit an empty
// form). Also returns the blockID→field-title mapping used to label
// rejection errors, so the view is walked exactly once. Pure, so form-layout
// learnings are table-testable on recorded view payloads.
func buildFormState(view map[string]any, schema WorkflowSchema, fields map[string]string) (map[string]any, map[string]string, error) {
	byAction, titlesByBlock := indexFormBlocks(view, schema)
	stateValues := map[string]any{}
	for title, value := range fields {
		field := schemaFieldByTitle(schema, title)
		if field == nil {
			return nil, nil, missingFormFieldError(title)
		}
		block, ok := byAction[field.Name]
		if !ok {
			return nil, nil, missingFormFieldError(title)
		}
		entry, err := formStateEntry(block.element, field.Title, value)
		if err != nil {
			return nil, nil, err
		}
		stateValues[block.blockID] = map[string]any{field.Name: entry}
	}
	return stateValues, titlesByBlock, nil
}

type formBlock struct {
	blockID string
	element map[string]any
}

// indexFormBlocks walks the view's blocks once: each input element's
// action_id keys its block, and blocks that resolve to a schema field record
// the blockID→title mapping.
func indexFormBlocks(view map[string]any, schema WorkflowSchema) (map[string]formBlock, map[string]string) {
	byAction := map[string]formBlock{}
	titlesByBlock := map[string]string{}
	for _, block := range recItems(getArr(view, "blocks")) {
		blockID := getStr(block, "block_id")
		element := getRec(block, "element")
		actionID := getStr(element, "action_id")
		if blockID == "" || actionID == "" {
			continue
		}
		byAction[actionID] = formBlock{blockID: blockID, element: element}
		if field := findSchemaField(schema, actionID); field != nil {
			titlesByBlock[blockID] = field.Title
		}
	}
	return byAction, titlesByBlock
}

func missingFormFieldError(title string) error {
	return agenterrors.Newf(agenterrors.FixableByRetry,
		"field %q is not present in the opened form — the view may not have loaded fully", title).
		WithHint("retry the run; 'workflow get' re-checks the field titles")
}

func findSchemaField(schema WorkflowSchema, actionID string) *FormField {
	for i := range schema.Fields {
		if schema.Fields[i].Name == actionID {
			return &schema.Fields[i]
		}
	}
	return nil
}

func schemaFieldByTitle(schema WorkflowSchema, title string) *FormField {
	for i := range schema.Fields {
		if strings.EqualFold(schema.Fields[i].Title, title) {
			return &schema.Fields[i]
		}
	}
	return nil
}

// formStateEntry builds one state entry in the shape the element's input type
// expects — views.submit rejects state whose type does not match the rendered
// element, and reports it only via response_action "errors".
func formStateEntry(element map[string]any, title, value string) (map[string]any, error) {
	elemType := FirstNonEmpty(getStr(element, "type"), "plain_text_input")
	switch elemType {
	case "plain_text_input", "number_input", "email_text_input", "url_text_input":
		return map[string]any{"type": elemType, "value": value}, nil
	case "rich_text_input":
		return map[string]any{"type": elemType, "rich_text_value": richTextValue(value)}, nil
	case "static_select", "radio_buttons":
		opt, err := matchElementOption(element, title, value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": elemType, "selected_option": opt}, nil
	case "checkboxes":
		var opts []any
		for _, part := range strings.Split(value, ",") {
			opt, err := matchElementOption(element, title, strings.TrimSpace(part))
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
				WithHint("use YYYY-MM-DD and rerun — this run was abandoned without submitting")
		}
		return map[string]any{"type": elemType, "selected_date": value}, nil
	case "timepicker":
		if _, err := time.Parse("15:04", value); err != nil {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"field %q expects a time, got %q", title, value).
				WithHint("use HH:MM (24h) and rerun — this run was abandoned without submitting")
		}
		return map[string]any{"type": elemType, "selected_time": value}, nil
	default:
		return nil, agenterrors.Newf(agenterrors.FixableByHuman,
			"field %q is a %s input, which agent-slack cannot submit", title, elemType).
			WithHint("this run was abandoned without submitting; use a Slack client for this workflow's form")
	}
}

// richTextValue wraps a plain string in the minimal rich_text document a
// rich_text_input element expects.
func richTextValue(value string) map[string]any {
	return map[string]any{
		"type": "rich_text",
		"elements": []any{map[string]any{
			"type": "rich_text_section",
			"elements": []any{map[string]any{"type": "text", "text": value}},
		}},
	}
}

// matchElementOption finds the element option whose value or label matches
// (labels case-insensitively) and returns the option object verbatim —
// views.submit expects the full option, text object included. Grouped options
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
		WithHint("match an option by its label or value and rerun — this run was abandoned without submitting")
}

func hasField(fields map[string]string, title string) bool {
	if _, ok := fields[title]; ok {
		return true
	}
	for k := range fields {
		if strings.EqualFold(k, title) {
			return true
		}
	}
	return false
}
