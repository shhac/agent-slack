package cli

import (
	"encoding/json"
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
