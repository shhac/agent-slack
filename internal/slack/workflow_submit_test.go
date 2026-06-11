package slack

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

type fakeRTM struct {
	messages []map[string]any
	closed   bool
}

func (f *fakeRTM) ReadJSON(ctx context.Context) (map[string]any, error) {
	if len(f.messages) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	msg := f.messages[0]
	f.messages = f.messages[1:]
	return msg, nil
}

func (f *fakeRTM) Close() { f.closed = true }

func testSchema() WorkflowSchema {
	return WorkflowSchema{
		WorkflowID: "Wf001",
		Fields: []FormField{
			{Name: "field-uuid-1", Title: "Summary", Type: "string", Required: true},
			{Name: "field-uuid-2", Title: "Priority", Type: "string"},
		},
	}
}

func TestValidateWorkflowFields(t *testing.T) {
	schema := testSchema()
	if errs := ValidateWorkflowFields(map[string]string{"summary": "x"}, schema); len(errs) != 0 {
		t.Errorf("case-insensitive title should validate: %v", errs)
	}
	errs := ValidateWorkflowFields(map[string]string{"Nope": "x"}, schema)
	if len(errs) != 2 { // unknown field + missing required Summary
		t.Errorf("errs = %v", errs)
	}
	if !strings.Contains(errs[0], "Available: Summary, Priority") {
		t.Errorf("unknown-field error should enumerate titles: %v", errs[0])
	}
}

func TestSubmitWorkflowForm(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("rtm.connect", map[string]any{"ok": true, "url": "wss://rtm.example/ws"})
	server.HandleBody("workflows.triggers.trip", map[string]any{
		"ok": true, "function_execution_id": "Fx1", "trigger_execution_id": "Tx1",
	})
	server.HandleBody("views.submit", map[string]any{"ok": true})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	fake := &fakeRTM{messages: []map[string]any{
		{"type": "hello"},
		{"type": "view_opened", "view": map[string]any{
			"id": "V123",
			"blocks": []any{map[string]any{
				"block_id": "blk1",
				"element":  map[string]any{"action_id": "field-uuid-1"},
			}},
		}},
	}}
	prev := dialRTM
	var gotCookie string
	dialRTM = func(ctx context.Context, wsURL, cookie string) (rtmConn, error) {
		gotCookie = cookie
		if wsURL != "wss://rtm.example/ws" {
			t.Errorf("wsURL = %q", wsURL)
		}
		return fake, nil
	}
	t.Cleanup(func() { dialRTM = prev })

	c := New(Auth{Type: AuthBrowser, XOXC: "xoxc-1", XOXD: "xoxd/c", WorkspaceURL: ts.URL})
	result, err := SubmitWorkflowForm(context.Background(), c, WorkflowSubmission{
		ShortcutURL: "https://slack.com/shortcuts/Ft0001/abc",
		ChannelID:   "C1",
		BookmarkID:  "Bk1",
		Fields:      map[string]string{"summary": "deploy failed"},
		Schema:      testSchema(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Submitted || result.ViewID != "V123" || result.FunctionExecutionID != "Fx1" {
		t.Errorf("result = %+v", result)
	}
	if !strings.Contains(gotCookie, "d=xoxd%2Fc") {
		t.Errorf("cookie = %q", gotCookie)
	}
	if !fake.closed {
		t.Error("RTM connection not closed")
	}

	submit := server.CallsFor("views.submit")[0]
	state := submit.Params.Get("state")
	if !strings.Contains(state, `"blk1"`) || !strings.Contains(state, "deploy failed") {
		t.Errorf("state = %q", state)
	}
}

func TestSubmitWorkflowFormRequiresBrowserAuth(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "xoxb-1"})
	_, err := SubmitWorkflowForm(context.Background(), c, WorkflowSubmission{Schema: testSchema()})
	if err == nil || !strings.Contains(err.Error(), "browser auth") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildFormState(t *testing.T) {
	view := map[string]any{
		"blocks": []any{
			map[string]any{"block_id": "blk1", "element": map[string]any{"action_id": "field-uuid-1"}},
			map[string]any{"block_id": "blk2", "element": map[string]any{"action_id": "field-uuid-2"}},
			map[string]any{"block_id": "blk3", "element": map[string]any{"action_id": "unknown-uuid"}},
			map[string]any{"element": map[string]any{"action_id": "field-uuid-1"}}, // no block_id
		},
	}
	state := buildFormState(view, testSchema(), map[string]string{"summary": "deploy failed"})
	if len(state) != 1 { // only blk1: blk2 has no user value, blk3 unknown field
		t.Fatalf("state = %v", state)
	}
	entry := state["blk1"].(map[string]any)["field-uuid-1"].(map[string]any)
	if entry["value"] != "deploy failed" || entry["type"] != "plain_text_input" {
		t.Errorf("entry = %v", entry)
	}
}
