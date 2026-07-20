package slack

import (
	"context"
	"encoding/json"
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

// stubEventView is the view carried on the view_opened event: id but no
// element types, as pushed when another client shares the session.
func stubEventView() map[string]any {
	return map[string]any{
		"id": "V123",
		"blocks": []any{map[string]any{
			"block_id": "blk1",
			"element":  map[string]any{"action_id": "field-uuid-1"},
		}},
	}
}

// installFakeRTM points dialRTM at a fake and restores it after the test.
func installFakeRTM(t *testing.T, fake *fakeRTM, wantURL string) *string {
	t.Helper()
	prev := dialRTM
	var gotCookie string
	dialRTM = func(ctx context.Context, wsURL, cookie string) (rtmConn, error) {
		gotCookie = cookie
		if wsURL != wantURL {
			t.Errorf("wsURL = %q", wsURL)
		}
		return fake, nil
	}
	t.Cleanup(func() { dialRTM = prev })
	return &gotCookie
}

func newSubmitServer(t *testing.T) *mockslack.Server {
	t.Helper()
	server := mockslack.New()
	server.HandleBody("rtm.connect", map[string]any{"ok": true, "url": "wss://rtm.example/ws"})
	server.HandleBody("workflows.triggers.trip", map[string]any{
		"ok": true, "function_execution_id": "Fx1", "trigger_execution_id": "Tx1",
	})
	return server
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
	server := newSubmitServer(t)
	// The fetched view is authoritative: it carries the element types the
	// event stub lacks (here a rich_text_input for the Summary field).
	server.HandleBody("views.get", map[string]any{"ok": true, "view": map[string]any{
		"id": "V123",
		"blocks": []any{map[string]any{
			"block_id": "blk1",
			"element":  map[string]any{"action_id": "field-uuid-1", "type": "rich_text_input"},
		}},
	}})
	server.HandleBody("views.submit", map[string]any{"ok": true, "view": nil, "response_action": "clear"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	fake := &fakeRTM{messages: []map[string]any{
		{"type": "hello"},
		{"type": "view_opened", "view": stubEventView()},
	}}
	gotCookie := installFakeRTM(t, fake, "wss://rtm.example/ws")

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
	if result.ResponseAction != "clear" {
		t.Errorf("ResponseAction = %q", result.ResponseAction)
	}
	if !strings.Contains(*gotCookie, "d=xoxd%2Fc") {
		t.Errorf("cookie = %q", *gotCookie)
	}
	if !fake.closed {
		t.Error("RTM connection not closed")
	}

	submit := server.CallsFor("views.submit")[0]
	var state map[string]any
	if err := json.Unmarshal([]byte(submit.Params.Get("state")), &state); err != nil {
		t.Fatal(err)
	}
	entry := getRec(getRec(getRec(state, "values"), "blk1"), "field-uuid-1")
	if getStr(entry, "type") != "rich_text_input" {
		t.Errorf("entry should follow the fetched view's element type: %v", entry)
	}
	if !strings.Contains(submit.Params.Get("state"), "deploy failed") {
		t.Errorf("state = %q", submit.Params.Get("state"))
	}
}

func TestSubmitWorkflowFormViewsGetFallback(t *testing.T) {
	server := newSubmitServer(t) // no views.get fixture: unknown_method error
	server.HandleBody("views.submit", map[string]any{"ok": true, "response_action": "clear"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	fake := &fakeRTM{messages: []map[string]any{
		{"type": "view_opened", "view": stubEventView()},
	}}
	installFakeRTM(t, fake, "wss://rtm.example/ws")

	c := New(Auth{Type: AuthBrowser, XOXC: "xoxc-1", XOXD: "xoxd/c", WorkspaceURL: ts.URL})
	result, err := SubmitWorkflowForm(context.Background(), c, WorkflowSubmission{
		ShortcutURL: "https://slack.com/shortcuts/Ft0001/abc",
		ChannelID:   "C1",
		Fields:      map[string]string{"Summary": "deploy failed"},
		Schema:      testSchema(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Submitted {
		t.Errorf("result = %+v", result)
	}
	state := server.CallsFor("views.submit")[0].Params.Get("state")
	if !strings.Contains(state, `"blk1"`) || !strings.Contains(state, "plain_text_input") {
		t.Errorf("event-view fallback should submit the stub's fields as plain text: %q", state)
	}
}

func TestSubmitWorkflowFormRejected(t *testing.T) {
	server := newSubmitServer(t)
	server.HandleBody("views.submit", map[string]any{
		"ok":              true,
		"response_action": "errors",
		"errors":          map[string]any{"blk1": "value is not valid for this field"},
	})
	server.HandleBody("views.close", map[string]any{"ok": true})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	fake := &fakeRTM{messages: []map[string]any{
		{"type": "view_opened", "view": stubEventView()},
	}}
	installFakeRTM(t, fake, "wss://rtm.example/ws")

	c := New(Auth{Type: AuthBrowser, XOXC: "xoxc-1", XOXD: "xoxd/c", WorkspaceURL: ts.URL})
	_, err := SubmitWorkflowForm(context.Background(), c, WorkflowSubmission{
		ShortcutURL: "https://slack.com/shortcuts/Ft0001/abc",
		ChannelID:   "C1",
		Fields:      map[string]string{"Summary": "deploy failed"},
		Schema:      testSchema(),
	})
	if err == nil {
		t.Fatal("ok:true + response_action errors must not report success")
	}
	if !strings.Contains(err.Error(), "Summary") || !strings.Contains(err.Error(), "value is not valid") {
		t.Errorf("error should name the field title and Slack's message: %v", err)
	}
	if len(server.CallsFor("views.close")) != 1 {
		t.Error("a rejected submission should abandon the view")
	}
}

func TestSubmitWorkflowFormBadFieldValueAbandonsView(t *testing.T) {
	server := newSubmitServer(t)
	server.HandleBody("views.get", map[string]any{"ok": true, "view": map[string]any{
		"id": "V123",
		"blocks": []any{map[string]any{
			"block_id": "blk1",
			"element": map[string]any{
				"action_id": "field-uuid-1",
				"type":      "static_select",
				"options": []any{map[string]any{
					"text":  map[string]any{"type": "plain_text", "text": "Low"},
					"value": "opt-low",
				}},
			},
		}},
	}})
	server.HandleBody("views.close", map[string]any{"ok": true})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	fake := &fakeRTM{messages: []map[string]any{
		{"type": "view_opened", "view": stubEventView()},
	}}
	installFakeRTM(t, fake, "wss://rtm.example/ws")

	c := New(Auth{Type: AuthBrowser, XOXC: "xoxc-1", XOXD: "xoxd/c", WorkspaceURL: ts.URL})
	_, err := SubmitWorkflowForm(context.Background(), c, WorkflowSubmission{
		ShortcutURL: "https://slack.com/shortcuts/Ft0001/abc",
		ChannelID:   "C1",
		Fields:      map[string]string{"Summary": "no-such-option"},
		Schema:      testSchema(),
	})
	if err == nil || !strings.Contains(err.Error(), "no option matching") {
		t.Fatalf("err = %v", err)
	}
	if len(server.CallsFor("views.submit")) != 0 {
		t.Error("nothing should be submitted after a field error")
	}
	closes := server.CallsFor("views.close")
	if len(closes) != 1 || closes[0].Params.Get("view_id") != "V123" {
		t.Errorf("a post-trip field error must abandon the opened view: %v", closes)
	}
}

func TestSubmitWorkflowFormRequiresBrowserAuth(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "xoxb-1"})
	_, err := SubmitWorkflowForm(context.Background(), c, WorkflowSubmission{Schema: testSchema()})
	if err == nil || !strings.Contains(err.Error(), "browser auth") {
		t.Fatalf("err = %v", err)
	}
}

type errRTM struct{}

func (errRTM) ReadJSON(ctx context.Context) (map[string]any, error) {
	return nil, context.Canceled
}
func (errRTM) Close() {}

func TestAwaitOpenedViewReadError(t *testing.T) {
	c := New(Auth{Type: AuthBrowser, XOXC: "xoxc-1", XOXD: "xoxd"})
	tripped := false
	_, err := awaitOpenedView(context.Background(), c, errRTM{}, func() error {
		tripped = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for the workflow form") {
		t.Fatalf("err = %v", err)
	}
	if !tripped {
		t.Error("trip must fire even when the connection is broken — the error surfaces on the wait")
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
	state, err := buildFormState(view, testSchema(), map[string]string{"summary": "deploy failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 1 { // only blk1: blk2 has no user value, blk3 unknown field
		t.Fatalf("state = %v", state)
	}
	entry := state["blk1"].(map[string]any)["field-uuid-1"].(map[string]any)
	if entry["value"] != "deploy failed" || entry["type"] != "plain_text_input" {
		t.Errorf("entry = %v", entry)
	}
}

func TestBuildFormStateUnmatchedField(t *testing.T) {
	view := map[string]any{"blocks": []any{}} // stub view: no blocks at all
	_, err := buildFormState(view, testSchema(), map[string]string{"summary": "x"})
	if err == nil || !strings.Contains(err.Error(), "not present in the opened form") {
		t.Fatalf("a supplied field with no matching block must error, got %v", err)
	}
}

func selectElement(elemType string) map[string]any {
	return map[string]any{
		"type": elemType,
		"options": []any{
			map[string]any{
				"text":  map[string]any{"type": "plain_text", "text": "Low"},
				"value": "opt-low",
			},
			map[string]any{
				"text":  map[string]any{"type": "plain_text", "text": "High"},
				"value": "opt-high",
			},
		},
	}
}

func TestFormStateEntryTypes(t *testing.T) {
	t.Run("rich text wraps the value in a rich_text document", func(t *testing.T) {
		entry, err := formStateEntry(map[string]any{"type": "rich_text_input"}, "Notes", "hello")
		if err != nil {
			t.Fatal(err)
		}
		doc := entry["rich_text_value"].(map[string]any)
		section := doc["elements"].([]any)[0].(map[string]any)
		text := section["elements"].([]any)[0].(map[string]any)
		if doc["type"] != "rich_text" || section["type"] != "rich_text_section" || text["text"] != "hello" {
			t.Errorf("entry = %v", entry)
		}
	})

	t.Run("select matches by label case-insensitively and copies the option verbatim", func(t *testing.T) {
		entry, err := formStateEntry(selectElement("static_select"), "Urgency", "low")
		if err != nil {
			t.Fatal(err)
		}
		opt := entry["selected_option"].(map[string]any)
		if opt["value"] != "opt-low" || getStr(getRec(opt, "text"), "text") != "Low" {
			t.Errorf("selected_option must be the element's option object: %v", opt)
		}
	})

	t.Run("select matches by option value", func(t *testing.T) {
		entry, err := formStateEntry(selectElement("radio_buttons"), "Urgency", "opt-high")
		if err != nil {
			t.Fatal(err)
		}
		if entry["selected_option"].(map[string]any)["value"] != "opt-high" {
			t.Errorf("entry = %v", entry)
		}
	})

	t.Run("unmatched option lists the available labels", func(t *testing.T) {
		_, err := formStateEntry(selectElement("static_select"), "Urgency", "Medium")
		if err == nil || !strings.Contains(err.Error(), "Low, High") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("grouped options are flattened", func(t *testing.T) {
		element := map[string]any{
			"type": "static_select",
			"option_groups": []any{map[string]any{
				"options": []any{map[string]any{
					"text":  map[string]any{"type": "plain_text", "text": "Grouped"},
					"value": "opt-grouped",
				}},
			}},
		}
		entry, err := formStateEntry(element, "Category", "Grouped")
		if err != nil {
			t.Fatal(err)
		}
		if entry["selected_option"].(map[string]any)["value"] != "opt-grouped" {
			t.Errorf("entry = %v", entry)
		}
	})

	t.Run("checkboxes split on commas", func(t *testing.T) {
		entry, err := formStateEntry(selectElement("checkboxes"), "Tags", "Low, opt-high")
		if err != nil {
			t.Fatal(err)
		}
		opts := entry["selected_options"].([]any)
		if len(opts) != 2 {
			t.Fatalf("opts = %v", opts)
		}
	})

	t.Run("datepicker validates the format", func(t *testing.T) {
		entry, err := formStateEntry(map[string]any{"type": "datepicker"}, "Due", "2026-01-31")
		if err != nil || entry["selected_date"] != "2026-01-31" {
			t.Fatalf("entry = %v err = %v", entry, err)
		}
		if _, err := formStateEntry(map[string]any{"type": "datepicker"}, "Due", "31/01/2026"); err == nil {
			t.Fatal("non-ISO date must error")
		}
	})

	t.Run("unsupported element types error instead of guessing a shape", func(t *testing.T) {
		_, err := formStateEntry(map[string]any{"type": "file_input"}, "Attachment", "x")
		if err == nil || !strings.Contains(err.Error(), "file_input") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSubmitRejection(t *testing.T) {
	view := stubEventView()
	if err := submitRejection(map[string]any{"ok": true, "response_action": "clear"}, view, testSchema()); err != nil {
		t.Fatalf("clear is success, got %v", err)
	}
	err := submitRejection(map[string]any{
		"ok":              true,
		"response_action": "errors",
		"errors":          map[string]any{"blk1": "too long", "blk-unknown": "missing"},
	}, view, testSchema())
	if err == nil {
		t.Fatal("expected rejection error")
	}
	// blk1 maps to the schema title; unmapped block ids fall back to the raw id.
	if !strings.Contains(err.Error(), "Summary: too long") || !strings.Contains(err.Error(), "blk-unknown: missing") {
		t.Errorf("err = %v", err)
	}
}
