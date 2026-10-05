package cli

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

const (
	actionChannel = "C0123ABCD"
	actionTS      = "1770165109.628379"
)

// actionCLIFixture is a browser-auth fixture whose RTM socket stays open and
// pushes onPress frames only once blocks.actions is called — the response an
// app makes to a press must arrive after it, never at connect time.
func actionCLIFixture(t *testing.T, preScript []map[string]any, onPress ...map[string]any) *cliFixture {
	t.Helper()
	f := newBrowserCLIFixture(t)
	f.server.EnableWebSocket(mockslack.WSScript{
		Frames:     append([]map[string]any{mockslack.Hello()}, preScript...),
		KeepOpen:   true,
		PushOnCall: map[string][]map[string]any{"blocks.actions": onPress},
	})
	f.server.HandleBody("rtm.connect", map[string]any{"ok": true, "url": mockslack.WebSocketURLFor(f.url)})
	f.server.HandleBody("blocks.actions", map[string]any{"ok": true})
	return f
}

// anyParams makes a HandleWhen override the fixture's default response.
func anyParams(url.Values) bool { return true }

func pressArgs(extra ...string) []string {
	return append([]string{"message", "action", actionChannel, "--ts", actionTS}, extra...)
}

func updatedCardMessage(ts string) map[string]any {
	msg := appCardMessage(ts)
	msg["text"] = "Deploy 42 approved"
	msg["blocks"] = []any{map[string]any{"type": "section", "block_id": "summary",
		"text": map[string]any{"type": "mrkdwn", "text": "Deploy 42 approved by @someone"}}}
	msg["edited"] = map[string]any{"user": "B0000000001", "ts": "1770165200.000100"}
	return msg
}

func TestMessageActionRequiresYes(t *testing.T) {
	f := actionCLIFixture(t, nil)
	f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))

	_, stderr, err := f.run(t, pressArgs("approve")...)
	if err == nil {
		t.Fatal("expected the press to be gated")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" || !strings.Contains(payload["hint"].(string), "--yes") {
		t.Errorf("payload = %v", payload)
	}
	if msg := payload["error"].(string); !strings.Contains(msg, `"Approve"`) || !strings.Contains(msg, "deploy-bot") {
		t.Errorf("preview %q should name the button and the app", msg)
	}
	for _, method := range []string{"blocks.actions", "rtm.connect"} {
		if n := len(f.server.CallsFor(method)); n != 0 {
			t.Errorf("%s called %d times without --yes", method, n)
		}
	}
}

func TestMessageActionPreviewCarriesConfirmWarning(t *testing.T) {
	f := actionCLIFixture(t, nil)
	card := appCardMessage(actionTS)
	button := card["blocks"].([]any)[1].(map[string]any)["elements"].([]any)[0].(map[string]any)
	button["confirm"] = map[string]any{
		"title": map[string]any{"type": "plain_text", "text": "Ship it?"},
		"text":  map[string]any{"type": "plain_text", "text": "This deploys to production."},
	}
	f.server.HandleBody("conversations.history", historyWith(card))

	_, stderr, _ := f.run(t, pressArgs("Approve")...)
	if msg := errPayload(t, stderr)["error"].(string); !strings.Contains(msg, "This deploys to production.") {
		t.Errorf("preview %q should carry the confirm dialog Slack would have shown", msg)
	}
}

// A bot token can never press, so it fails before asking anyone to confirm.
func TestMessageActionNeedsBrowserAuthBeforeTheGate(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))

	_, stderr, err := f.run(t, pressArgs("Approve")...)
	if err == nil {
		t.Fatal("expected an auth error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" || !strings.Contains(payload["error"].(string), "browser auth") {
		t.Errorf("payload = %v, want the browser-auth error rather than the --yes gate", payload)
	}
}

func TestMessageActionSelectionErrors(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"unknown label": {[]string{"Launch"}, `"Approve" (button decide/approve)`},
		"no selector":   {nil, "pass its label or --action-id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := actionCLIFixture(t, nil)
			f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))
			_, stderr, err := f.run(t, pressArgs(append(tc.args, "--yes")...)...)
			if err == nil {
				t.Fatal("expected a selection error")
			}
			payload := errPayload(t, stderr)
			if payload["fixable_by"] != "agent" || !strings.Contains(payload["hint"].(string), tc.want) {
				t.Errorf("payload = %v, want hint containing %q", payload, tc.want)
			}
			if len(f.server.CallsFor("blocks.actions")) != 0 {
				t.Error("nothing should be pressed when selection fails")
			}
		})
	}
}

func TestMessageActionPressUpdatesTheCard(t *testing.T) {
	f := actionCLIFixture(t, nil,
		mockslack.WSMessageChanged(actionChannel, "B0000000001", "Deploy 42 approved", actionTS, "1770165200.000100"))
	f.server.Handle("conversations.history",
		mockslack.Response{Body: historyWith(appCardMessage(actionTS))},
		mockslack.Response{Body: historyWith(updatedCardMessage(actionTS))})

	stdout, _, err := f.run(t, pressArgs("approve", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["pressed"] != true || payload["outcome"] != "message_updated" {
		t.Fatalf("payload = %v", payload)
	}
	if content := payload["message"].(map[string]any)["content"].(string); !strings.Contains(content, "approved") {
		t.Errorf("message content = %q, want the updated card", content)
	}

	calls := f.server.CallsFor("blocks.actions")
	if len(calls) != 1 {
		t.Fatalf("blocks.actions called %d times", len(calls))
	}
	p := calls[0].Params
	if p.Get("service_id") != "B0000000001" || p.Get("service_team_id") != "T0000000001" || p.Get("client_token") == "" {
		t.Errorf("params = %v", p)
	}
	var actions []map[string]any
	if err := json.Unmarshal([]byte(p.Get("actions")), &actions); err != nil || len(actions) != 1 {
		t.Fatalf("actions = %q (%v)", p.Get("actions"), err)
	}
	if a := actions[0]; a["action_id"] != "approve" || a["block_id"] != "decide" || a["type"] != "button" || a["value"] != "deploy-42" {
		t.Errorf("action = %v", a)
	}
	var container map[string]any
	if err := json.Unmarshal([]byte(p.Get("container")), &container); err != nil {
		t.Fatal(err)
	}
	if container["type"] != "message" || container["message_ts"] != actionTS || container["channel_id"] != actionChannel || container["is_ephemeral"] != false {
		t.Errorf("container = %v", container)
	}
}

func TestMessageActionDescribesAndClosesAnOpenedForm(t *testing.T) {
	view := map[string]any{
		"id": "V0000000001", "app_id": "A0000000001",
		"title": map[string]any{"type": "plain_text", "text": "Edit update"},
		"blocks": []any{
			map[string]any{"type": "input", "block_id": "msg",
				"label":   map[string]any{"type": "plain_text", "text": "Message"},
				"element": map[string]any{"type": "plain_text_input", "action_id": "text"}},
			map[string]any{"type": "input", "block_id": "sev", "optional": true,
				"label": map[string]any{"type": "plain_text", "text": "Severity"},
				"element": map[string]any{"type": "static_select", "action_id": "pick", "options": []any{
					map[string]any{"text": map[string]any{"type": "plain_text", "text": "Minor"}, "value": "minor"},
				}}},
		},
	}
	f := actionCLIFixture(t, nil, mockslack.WSViewOpened(map[string]any{"id": "V0000000001"}))
	f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))
	f.server.HandleBody("views.get", map[string]any{"ok": true, "view": view})
	f.server.HandleBody("views.close", map[string]any{"ok": true})

	stdout, _, err := f.run(t, pressArgs("Edit", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["outcome"] != "view_opened" {
		t.Fatalf("payload = %v", payload)
	}
	got := payload["view"].(map[string]any)
	if got["title"] != "Edit update" || got["closed"] != true {
		t.Errorf("view = %v", got)
	}
	fields := got["fields"].([]any)
	if len(fields) != 2 || fields[0].(map[string]any)["required"] != true || fields[1].(map[string]any)["required"] != nil {
		t.Errorf("fields = %v", fields)
	}
	if closes := f.server.CallsFor("views.close"); len(closes) != 1 || closes[0].Params.Get("view_id") != "V0000000001" {
		t.Errorf("views.close calls = %v", closes)
	}
}

// An edit already in flight when the socket connects is not the press's
// response: only a re-read that differs from the pre-press copy counts.
func TestMessageActionIgnoresAnUnchangedCard(t *testing.T) {
	f := actionCLIFixture(t,
		[]map[string]any{mockslack.WSMessageChanged(actionChannel, "B0000000001", "Deploy 42 is waiting for approval", actionTS, "1770165100.000100")})
	f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))

	stdout, _, err := f.run(t, pressArgs("Dismiss", "--yes", "--wait", "1500ms")...)
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["outcome"] != "none" || payload["message"] != nil {
		t.Errorf("payload = %v, want outcome none", payload)
	}
}

func TestMessageActionReportsADeletedCard(t *testing.T) {
	f := actionCLIFixture(t, nil, mockslack.WSMessageDeleted(actionChannel, actionTS, "1770165200.000100"))
	f.server.Handle("conversations.history",
		mockslack.Response{Body: historyWith(appCardMessage(actionTS))},
		mockslack.Response{Body: historyWith()})
	f.server.HandleBody("conversations.replies", historyWith())

	stdout, _, err := f.run(t, pressArgs("Dismiss", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["outcome"] != "message_deleted" {
		t.Errorf("payload = %v", payload)
	}
}

func TestMessageActionWithoutWatching(t *testing.T) {
	f := actionCLIFixture(t, nil)
	f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))

	stdout, _, err := f.run(t, pressArgs("Approve", "--yes", "--wait", "0")...)
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["outcome"] != "unobserved" {
		t.Errorf("payload = %v", payload)
	}
	if len(f.server.CallsFor("rtm.connect")) != 0 || len(f.server.CallsFor("blocks.actions")) != 1 {
		t.Error("--wait 0 should press once without opening RTM")
	}
}

// editFormView is an app's pre-filled "edit" modal: the update text arrives
// as the current value, the way an incident bot's Edit button opens it.
func editFormView() map[string]any {
	return map[string]any{
		"id": "V0000000002", "app_id": "A0000000001",
		"title": map[string]any{"type": "plain_text", "text": "Edit update"},
		"blocks": []any{
			map[string]any{"type": "input", "block_id": "msg",
				"label": map[string]any{"type": "plain_text", "text": "Message"},
				"element": map[string]any{"type": "rich_text_input", "action_id": "input",
					"initial_value": map[string]any{"type": "rich_text", "elements": []any{
						map[string]any{"type": "rich_text_section", "elements": []any{
							map[string]any{"type": "text", "text": "We found the cause."},
						}},
					}}}},
			map[string]any{"type": "input", "block_id": "sev",
				"label": map[string]any{"type": "plain_text", "text": "Severity"},
				"element": map[string]any{"type": "static_select", "action_id": "input",
					"initial_option": map[string]any{"text": map[string]any{"type": "plain_text", "text": "Minor"}, "value": "minor"},
					"options": []any{
						map[string]any{"text": map[string]any{"type": "plain_text", "text": "Minor"}, "value": "minor"},
						map[string]any{"text": map[string]any{"type": "plain_text", "text": "Major"}, "value": "major"},
					}}},
		},
	}
}

func formFixture(t *testing.T) *cliFixture {
	t.Helper()
	f := actionCLIFixture(t, nil, mockslack.WSViewOpened(map[string]any{"id": "V0000000002"}))
	f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))
	f.server.HandleBody("views.get", map[string]any{"ok": true, "view": editFormView()})
	f.server.HandleBody("views.submit", map[string]any{"ok": true, "view": nil, "response_action": "clear"})
	f.server.HandleBody("views.close", map[string]any{"ok": true})
	return f
}

func TestMessageActionFillsTheOpenedForm(t *testing.T) {
	f := formFixture(t)

	stdout, _, err := f.run(t, pressArgs("Edit", "--field", "severity=Major", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	view := parseJSON(t, stdout)["view"].(map[string]any)
	if view["submitted"] != true || view["closed"] != true || view["error"] != nil {
		t.Fatalf("view = %v", view)
	}
	if fields := view["fields"].([]any); fields[0].(map[string]any)["value"] != "We found the cause." {
		t.Errorf("fields = %v, want the pre-filled message shown", fields)
	}

	submits := f.server.CallsFor("views.submit")
	if len(submits) != 1 || submits[0].Params.Get("view_id") != "V0000000002" {
		t.Fatalf("views.submit calls = %v", submits)
	}
	var state struct {
		Values map[string]map[string]map[string]any `json:"values"`
	}
	if err := json.Unmarshal([]byte(submits[0].Params.Get("state")), &state); err != nil {
		t.Fatal(err)
	}
	// Both blocks reuse action_id "input": the state must keep them apart,
	// and the untouched message must go back as it was, not empty.
	if _, kept := state.Values["msg"]["input"]["rich_text_value"]; !kept {
		t.Errorf("state = %v, want the pre-filled message resubmitted", state.Values)
	}
	if opt := state.Values["sev"]["input"]["selected_option"].(map[string]any); opt["value"] != "major" {
		t.Errorf("severity = %v", opt)
	}
	if len(f.server.CallsFor("views.close")) != 0 {
		t.Error("an accepted submission closes itself")
	}
}

func TestMessageActionClosesAFormItCannotFill(t *testing.T) {
	f := formFixture(t)

	stdout, _, err := f.run(t, pressArgs("Edit", "--field", "Owner=U0000000009", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	view := parseJSON(t, stdout)["view"].(map[string]any)
	if msg, _ := view["error"].(string); !strings.Contains(msg, `no field "Owner"`) || view["submitted"] != false || view["closed"] != true {
		t.Errorf("view = %v", view)
	}
	if len(f.server.CallsFor("views.submit")) != 0 {
		t.Error("nothing should be submitted for an unknown field")
	}
}

func TestMessageActionWarnsWhenNoFormOpens(t *testing.T) {
	f := actionCLIFixture(t, nil)
	f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))

	stdout, _, err := f.run(t, pressArgs("Approve", "--field", "Message=x", "--wait", "1s", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	warnings, _ := parseJSON(t, stdout)["warnings"].([]any)
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "opened no form") {
		t.Errorf("warnings = %v", warnings)
	}
}

func menuCard(ts string) map[string]any {
	msg := appCardMessage(ts)
	msg["blocks"] = []any{map[string]any{"type": "actions", "block_id": "route", "elements": []any{
		map[string]any{"type": "static_select", "action_id": "env",
			"placeholder": map[string]any{"type": "plain_text", "text": "Environment"},
			"options": []any{
				map[string]any{"text": map[string]any{"type": "plain_text", "text": "Staging"}, "value": "stg"},
				map[string]any{"text": map[string]any{"type": "plain_text", "text": "Production"}, "value": "prd"},
			}},
	}}}
	return msg
}

func TestMessageActionChoosesAMenuOption(t *testing.T) {
	f := actionCLIFixture(t, nil)
	f.server.HandleBody("conversations.history", historyWith(menuCard(actionTS)))

	_, stderr, _ := f.run(t, pressArgs("Environment", "--value", "production")...)
	if msg := errPayload(t, stderr)["error"].(string); !strings.Contains(msg, `choose "production"`) {
		t.Errorf("preview %q should name the choice", msg)
	}

	if _, _, err := f.run(t, pressArgs("Environment", "--value", "production", "--wait", "0", "--yes")...); err != nil {
		t.Fatal(err)
	}
	var actions []map[string]any
	if err := json.Unmarshal([]byte(f.server.CallsFor("blocks.actions")[0].Params.Get("actions")), &actions); err != nil {
		t.Fatal(err)
	}
	if opt, _ := actions[0]["selected_option"].(map[string]any); opt["value"] != "prd" {
		t.Errorf("action = %v, want the Production option selected", actions[0])
	}
}

func TestMessageActionMenuNeedsAValue(t *testing.T) {
	f := actionCLIFixture(t, nil)
	f.server.HandleBody("conversations.history", historyWith(menuCard(actionTS)))

	_, stderr, err := f.run(t, pressArgs("env", "--yes")...)
	if err == nil {
		t.Fatal("expected an error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" || !strings.Contains(payload["hint"].(string), "Staging, Production") {
		t.Errorf("payload = %v", payload)
	}
	if len(f.server.CallsFor("blocks.actions")) != 0 {
		t.Error("nothing should be pressed without a choice")
	}
}

// Once blocks.actions is sent a retry presses again, so a failure the
// transport would call retryable must reach the agent as "check first".
func TestMessageActionFailedPressIsNotRetryable(t *testing.T) {
	for _, wait := range []string{"0", "1s"} {
		t.Run("wait "+wait, func(t *testing.T) {
			f := actionCLIFixture(t, nil)
			f.server.HandleBody("conversations.history", historyWith(appCardMessage(actionTS)))
			f.server.HandleWhen("blocks.actions", anyParams, mockslack.Response{Status: 500})

			_, stderr, err := f.run(t, pressArgs("Approve", "--wait", wait, "--yes")...)
			if err == nil {
				t.Fatal("expected the failed press to error")
			}
			payload := errPayload(t, stderr)
			if payload["fixable_by"] != "agent" || !strings.Contains(payload["hint"].(string), "message get") {
				t.Errorf("payload = %v, want agent-fixable with a check-first hint", payload)
			}
			if n := len(f.server.CallsFor("blocks.actions")); n != 1 {
				t.Errorf("blocks.actions called %d times", n)
			}
		})
	}
}

// With no successful re-read a card change cannot be ruled out: that is
// "unknown", never "none", or an agent would press again.
func TestMessageActionCannotSeeTheCard(t *testing.T) {
	f := actionCLIFixture(t, nil)
	f.server.Handle("conversations.history",
		mockslack.Response{Body: historyWith(appCardMessage(actionTS))},
		mockslack.Response{Status: 500})

	stdout, _, err := f.run(t, pressArgs("Approve", "--wait", "1s", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	warnings, _ := payload["warnings"].([]any)
	if payload["outcome"] != "unknown" || len(warnings) != 1 || !strings.Contains(warnings[0].(string), "rather than pressing again") {
		t.Errorf("payload = %v", payload)
	}
}

func TestMessageActionIgnoresAnotherAppsForm(t *testing.T) {
	card := appCardMessage(actionTS)
	card["bot_profile"].(map[string]any)["app_id"] = "A0000000001"
	f := actionCLIFixture(t, nil, mockslack.WSViewOpened(map[string]any{"id": "V0000000003", "app_id": "A0000000002"}))
	f.server.HandleBody("conversations.history", historyWith(card))

	stdout, _, err := f.run(t, pressArgs("Approve", "--wait", "1s", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["outcome"] != "none" || payload["view"] != nil {
		t.Errorf("payload = %v, want another app's form ignored", payload)
	}
	if len(f.server.CallsFor("views.close")) != 0 {
		t.Error("a form that is not the press's response must not be closed")
	}
}

func TestMessageActionKeepsTheMayHaveSubmittedHint(t *testing.T) {
	f := formFixture(t)
	f.server.HandleWhen("views.submit", anyParams, mockslack.Response{Status: 500})

	stdout, _, err := f.run(t, pressArgs("Edit", "--field", "Severity=Major", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	view := parseJSON(t, stdout)["view"].(map[string]any)
	if msg, _ := view["error"].(string); !strings.Contains(msg, "may have been submitted") {
		t.Errorf("view.error = %q, want the hint that stops a second submission", msg)
	}
}

// A submission that moves the form to another step leaves that step open on
// the user's other clients; it is closed and named.
func TestMessageActionClosesAFurtherFormStep(t *testing.T) {
	for _, action := range []string{"update", "push"} {
		t.Run(action, func(t *testing.T) {
			f := formFixture(t)
			f.server.HandleWhen("views.submit", anyParams, mockslack.Response{Body: map[string]any{"ok": true, "response_action": action}})

			stdout, _, err := f.run(t, pressArgs("Edit", "--field", "Severity=Major", "--yes")...)
			if err != nil {
				t.Fatal(err)
			}
			payload := parseJSON(t, stdout)
			view := payload["view"].(map[string]any)
			warnings, _ := payload["warnings"].([]any)
			if view["submitted"] != true || view["response_action"] != action || view["closed"] != true {
				t.Errorf("view = %v", view)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "another step") {
				t.Errorf("warnings = %v", warnings)
			}
			if n := len(f.server.CallsFor("views.close")); n != 1 {
				t.Errorf("views.close called %d times", n)
			}
		})
	}
}
