package cli

import (
	"testing"
)

func TestWorkflowListAndRun(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("bookmarks.list", map[string]any{
		"ok": true,
		"bookmarks": []any{map[string]any{
			"id": "Bk1", "title": "Deploy", "shortcut_id": "Ft0001",
			"link": "https://slack.com/shortcuts/Ft0001/abc",
		}},
	})
	f.server.HandleBody("workflows.featured.list", map[string]any{"ok": false, "error": "unknown_method"})

	out, _, err := f.run(t, "workflow", "list", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if lines[0]["trigger_id"] != "Ft0001" {
		t.Errorf("workflow = %v", lines[0])
	}

	f.server.HandleBody("workflows.triggers.trip", map[string]any{
		"ok": true, "function_execution_id": "Fx1", "trigger_execution_id": "Tx1",
	})
	out, _, err = f.run(t, "workflow", "run", "Ft0001", "--channel", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	run := parseJSON(t, out)["run"].(map[string]any)
	if run["function_execution_id"] != "Fx1" {
		t.Errorf("run = %v", run)
	}
	trip := f.server.CallsFor("workflows.triggers.trip")[0]
	if trip.Params.Get("url") != "https://slack.com/shortcuts/Ft0001/abc" {
		t.Errorf("trip params = %v", trip.Params)
	}
}

func TestWorkflowGetSchema(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("workflows.get", map[string]any{
		"ok": true,
		"workflow": map[string]any{
			"id": "Wf001", "title": "Request",
			"steps": []any{map[string]any{
				"function": map[string]any{"callback_id": "open_form", "title": "Collect info"},
				"inputs": map[string]any{
					"title": map[string]any{"value": "Request form"},
					"fields": map[string]any{"value": map[string]any{
						"elements": []any{map[string]any{"name": "field-uuid-1", "title": "Summary", "type": "string"}},
						"required": []any{"field-uuid-1"},
					}},
				},
			}},
		},
	})

	out, _, err := f.run(t, "workflow", "get", "Wf001")
	if err != nil {
		t.Fatal(err)
	}
	schema := parseJSON(t, out)
	fields := schema["fields"].([]any)
	field := fields[0].(map[string]any)
	if field["title"] != "Summary" || field["required"] != true {
		t.Errorf("field = %v", field)
	}
	if schema["form_title"] != "Request form" {
		t.Errorf("schema = %v", schema)
	}
}
