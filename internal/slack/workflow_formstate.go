// The pure form-state layer of workflow submission: mapping a rendered
// form view plus user-supplied Title=value pairs onto the state shape
// views.submit expects. No client, no network — table-testable on recorded
// view payloads. The effectful submit flow lives in workflow_submit.go
// (mirroring the workflows.go / workflows_schema.go split).
package slack

import (
	"fmt"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// ValidateWorkflowFields checks user-supplied Title=value pairs against the
// schema (case-insensitive on titles) and returns the problems found.
// schemaFieldByTitle is the one title-matching rule, so validation and
// submission cannot disagree about which titles match.
func ValidateWorkflowFields(fields map[string]string, schema WorkflowSchema) []string {
	var errs []string
	titles := make([]string, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		titles = append(titles, f.Title)
	}
	supplied := map[string]bool{}
	for title := range fields {
		field := schemaFieldByTitle(schema, title)
		if field == nil {
			errs = append(errs, fmt.Sprintf("unknown field %q. Available: %s", title, strings.Join(titles, ", ")))
			continue
		}
		supplied[field.Name] = true
	}
	for _, f := range schema.Fields {
		if f.Required && !supplied[f.Name] {
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
			// Not a loading problem — retrying re-trips the workflow and can
			// never succeed, so this routes to the agent, not to retry.
			return nil, nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"field %q is not in the workflow's schema", title).
				WithHint("'workflow get' lists the field titles; " + abandonedRunHint)
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

// abandonedRunHint states the contract owned by SubmitWorkflowForm's
// success-gated abandonView defer: any error after the trigger trips closes
// the opened form, cancelling that run.
const abandonedRunHint = "this run was abandoned without submitting"

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
