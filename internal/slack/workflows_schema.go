package slack

import (
	"context"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// FormField is one input of a workflow's open_form step.
type FormField struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Long        bool   `json:"long,omitempty"`
}

// WorkflowSchema is a workflow's definition: form fields + step titles.
type WorkflowSchema struct {
	WorkflowID  string      `json:"workflow_id"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	FormTitle   string      `json:"form_title,omitempty"`
	Fields      []FormField `json:"fields"`
	Steps       []string    `json:"steps"`
}

func GetWorkflowSchema(ctx context.Context, c *Client, workflowID string) (WorkflowSchema, error) {
	if s, ok := c.cachedWorkflowSchema(workflowID); ok {
		return s, nil
	}
	resp, err := c.API(ctx, "workflows.get", map[string]any{"workflow_id": workflowID})
	if err != nil {
		return WorkflowSchema{}, err
	}
	wf := getRec(resp, "workflow")
	if wf == nil {
		return WorkflowSchema{}, agenterrors.Newf(agenterrors.FixableByAgent, "no workflow found for ID %s", workflowID).
			WithHint("check the workflow id (Wf…); 'agent-slack workflow get <Ft-trigger>' resolves a trigger to its workflow")
	}

	schema := assembleWorkflowSchema(wf, workflowID)
	c.cacheWorkflowSchema(workflowID, schema)
	return schema, nil
}

// assembleWorkflowSchema shapes a raw workflows.get workflow object into the
// schema: step titles plus the open_form step's fields. Pure.
func assembleWorkflowSchema(wf map[string]any, workflowID string) WorkflowSchema {
	schema := WorkflowSchema{
		WorkflowID:  firstNonEmpty(getStr(wf, "id"), workflowID),
		Title:       getStr(wf, "title"),
		Description: getStr(wf, "description"),
		Fields:      []FormField{},
		Steps:       []string{},
	}
	for _, step := range recItems(getArr(wf, "steps")) {
		fn := getRec(step, "function")
		callbackID := getStr(fn, "callback_id")
		schema.Steps = append(schema.Steps, firstNonEmpty(getStr(fn, "title"), callbackID))

		if callbackID != "open_form" {
			continue
		}
		inputs := getRec(step, "inputs")
		schema.FormTitle = getStr(getRec(inputs, "title"), "value")
		schema.Fields = append(schema.Fields, extractFormFields(getRec(getRec(inputs, "fields"), "value"))...)
	}
	return schema
}

// extractFormFields shapes an open_form step's fields.value object — the
// element list plus its sibling required-names array — into FormFields.
func extractFormFields(fieldsValue map[string]any) []FormField {
	required := map[string]bool{}
	for _, r := range getArr(fieldsValue, "required") {
		if s, ok := r.(string); ok && s != "" {
			required[s] = true
		}
	}
	var fields []FormField
	for _, el := range recItems(getArr(fieldsValue, "elements")) {
		name := getStr(el, "name")
		fieldType := getStr(el, "type")
		if fieldType == "" {
			fieldType = "string"
		}
		fields = append(fields, FormField{
			Name:        name,
			Title:       getStr(el, "title"),
			Type:        fieldType,
			Description: getStr(el, "description"),
			Required:    required[name],
			Long:        getBool(el, "long"),
		})
	}
	return fields
}
