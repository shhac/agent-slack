package cli

import (
	"strings"
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
	f.server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok":       true,
		"triggers": []any{map[string]any{"id": "Ft0001", "workflow": map[string]any{"workflow_id": "Wf0001"}}},
	})

	out, _, err := f.run(t, "workflow", "list", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if lines[0]["trigger_id"] != "Ft0001" {
		t.Errorf("workflow = %v", lines[0])
	}
	if _, isStale := lines[0]["stale"]; isStale {
		t.Errorf("a validated trigger must not carry a stale flag: %v", lines[0])
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

// The pre-submission guardrails are the only thing standing between a
// malformed invocation and a real workflow trip — pin that they fire before
// any mutation.
func TestWorkflowRunFieldValidation(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok":       true,
		"triggers": []any{map[string]any{"id": "Ft0001", "workflow": map[string]any{"workflow_id": "Wf001"}}},
	})
	f.server.HandleBody("workflows.get", map[string]any{
		"ok": true,
		"workflow": map[string]any{
			"id": "Wf001",
			"steps": []any{map[string]any{
				"function": map[string]any{"callback_id": "open_form", "title": "Collect info"},
				"inputs": map[string]any{
					"fields": map[string]any{"value": map[string]any{
						"elements": []any{map[string]any{"name": "field-uuid-1", "title": "Summary", "type": "string"}},
						"required": []any{"field-uuid-1"},
					}},
				},
			}},
		},
	})

	_, stderr, err := f.run(t, "workflow", "run", "Ft0001", "--channel", "C12345678", "--field", "Nope=x")
	if err == nil {
		t.Fatal("unknown field must error")
	}
	payload := errPayload(t, stderr)
	msg := payload["error"].(string)
	if payload["fixable_by"] != "agent" || !strings.Contains(msg, `unknown field "Nope"`) || !strings.Contains(msg, "Summary") {
		t.Errorf("payload = %v", payload)
	}
	if !strings.Contains(payload["hint"].(string), "workflow get") {
		t.Errorf("hint should route the agent to the schema: %v", payload["hint"])
	}
	if len(f.server.CallsFor("workflows.triggers.trip")) != 0 {
		t.Error("validation failure must never trip the trigger")
	}
}

func TestWorkflowRunFieldFormat(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "workflow", "run", "Ft0001", "--channel", "C12345678", "--field", "badformat")
	if err == nil {
		t.Fatal("malformed --field must error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" || !strings.Contains(payload["error"].(string), "invalid --field format") {
		t.Errorf("payload = %v", payload)
	}
	if len(f.server.CallsFor("workflows.triggers.trip")) != 0 {
		t.Error("a malformed flag must never trip the trigger")
	}
}
