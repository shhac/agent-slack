package slack

import (
	"reflect"
	"testing"
)

// Direct tests for the pure payload shaping — Slack can return partial or
// malformed workflow objects, and these defaults are load-bearing for form
// submission.

func TestExtractFormFields(t *testing.T) {
	if got := extractFormFields(nil); len(got) != 0 {
		t.Errorf("nil fieldsValue: got %v", got)
	}
	if got := extractFormFields(map[string]any{"elements": []any{}}); len(got) != 0 {
		t.Errorf("empty elements: got %v", got)
	}

	fields := extractFormFields(map[string]any{
		"required": []any{"f1", 42, "", nil}, // non-strings and empties ignored
		"elements": []any{
			map[string]any{"name": "f1", "title": "Summary"}, // no type → "string"
			map[string]any{"name": "f2", "title": "Long", "type": "text", "long": true},
			"not-a-map", // ignored
		},
	})
	want := []FormField{
		{Name: "f1", Title: "Summary", Type: "string", Required: true},
		{Name: "f2", Title: "Long", Type: "text", Long: true},
	}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("got %+v\nwant %+v", fields, want)
	}
}

func TestAssembleWorkflowSchema(t *testing.T) {
	// Minimal object: id falls back to the requested workflowID, slices are
	// non-nil so they marshal as [] not null.
	schema := assembleWorkflowSchema(map[string]any{}, "Wf0FALLBACK")
	if schema.WorkflowID != "Wf0FALLBACK" || schema.Fields == nil || schema.Steps == nil {
		t.Errorf("minimal schema = %+v", schema)
	}

	schema = assembleWorkflowSchema(map[string]any{
		"id":    "Wf0REAL",
		"title": "Request",
		"steps": []any{
			map[string]any{"function": map[string]any{"callback_id": "open_form", "title": "Collect"},
				"inputs": map[string]any{
					"title":  map[string]any{"value": "The Form"},
					"fields": map[string]any{"value": map[string]any{"elements": []any{map[string]any{"name": "f1", "title": "T"}}}},
				}},
			map[string]any{"function": map[string]any{"callback_id": "send_message"}}, // no title → callback id
		},
	}, "Wf0IGNORED")
	if schema.WorkflowID != "Wf0REAL" || schema.FormTitle != "The Form" {
		t.Errorf("schema = %+v", schema)
	}
	if !reflect.DeepEqual(schema.Steps, []string{"Collect", "send_message"}) {
		t.Errorf("steps = %v", schema.Steps)
	}
	if len(schema.Fields) != 1 || schema.Fields[0].Name != "f1" {
		t.Errorf("fields = %+v", schema.Fields)
	}
}

func TestAssembleWorkflowPreview(t *testing.T) {
	preview := assembleWorkflowPreview(map[string]any{
		"type": "shortcut",
		"workflow": map[string]any{
			"title": "PRs",
			"app":   map[string]any{"id": "A0FROMAPP", "name": "Bot"},
		},
		"workflow_details": map[string]any{
			"collaborators": []any{"U0OK", "", 42, nil}, // non-strings/empties dropped
		},
	}, "Ft0FALLBACK")

	if preview.TriggerID != "Ft0FALLBACK" {
		t.Errorf("trigger id should fall back to the requested id: %q", preview.TriggerID)
	}
	if preview.Workflow.AppID != "A0FROMAPP" {
		t.Errorf("app id should fall back to app.id: %q", preview.Workflow.AppID)
	}
	if !reflect.DeepEqual(preview.Collaborators, []string{"U0OK"}) {
		t.Errorf("collaborators = %v", preview.Collaborators)
	}
}
