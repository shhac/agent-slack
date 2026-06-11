package cli

import (
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// The message-resolution fallback ladder encodes the core Slack learning:
// conversations.history does not guarantee thread replies.

func TestMessageGetReplyViaThreadHint(t *testing.T) {
	f := newCLIFixture(t)
	// History around the ts misses the reply…
	f.server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1770165000.000001", "U1", "unrelated"),
	))
	// …but the thread named by ?thread_ts= contains it.
	reply := mockslack.Message("1770165109.628379", "U2", "the reply")
	reply["thread_ts"] = "1770165000.000001"
	f.server.HandleBody("conversations.replies", mockslack.History(
		mockslack.Message("1770165000.000001", "U1", "root"),
		reply,
	))

	out, _, err := f.run(t, "message", "get",
		"https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379?thread_ts=1770165000.000001&cid=C1A2B3C4D")
	if err != nil {
		t.Fatal(err)
	}
	msg := parseJSON(t, out)["message"].(map[string]any)
	if msg["content"] != "the reply" || msg["thread_ts"] != "1770165000.000001" {
		t.Errorf("message = %v", msg)
	}
	replies := f.server.CallsFor("conversations.replies")
	if len(replies) == 0 || replies[0].Params.Get("ts") != "1770165000.000001" {
		t.Errorf("replies calls = %v (should scan the hinted thread)", replies)
	}
}

func TestMessageGetRootViaRepliesFallback(t *testing.T) {
	f := newCLIFixture(t)
	// History misses the message entirely (scrolled out), no thread hint…
	f.server.HandleBody("conversations.history", mockslack.History())
	// …but conversations.replies on the ts itself finds the thread root.
	root := mockslack.Message("1770165109.628379", "U1", "old thread root")
	root["thread_ts"] = "1770165109.628379"
	root["reply_count"] = float64(2)
	f.server.HandleBody("conversations.replies", mockslack.History(root))

	out, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["message"].(map[string]any)["content"] != "old thread root" {
		t.Errorf("payload = %v", payload)
	}
	if payload["thread"].(map[string]any)["length"] != float64(3) {
		t.Errorf("thread = %v", payload["thread"])
	}
}

func TestMessageListReplyPermalinkListsWholeThread(t *testing.T) {
	f := newCLIFixture(t)
	reply := mockslack.Message("1770165109.628379", "U2", "reply two")
	reply["thread_ts"] = "1770165000.000001"
	f.server.HandleBody("conversations.history", mockslack.History(reply))

	root := mockslack.Message("1770165000.000001", "U1", "root")
	root["thread_ts"] = "1770165000.000001"
	root["reply_count"] = float64(1)
	f.server.HandleBody("conversations.replies", mockslack.History(root, reply))

	out, _, err := f.run(t, "message", "list", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 4 { // root + reply + @channel_id + @thread_ts
		t.Fatalf("lines = %v", lines)
	}
	if lines[0]["content"] != "root" || lines[1]["content"] != "reply two" {
		t.Errorf("thread order = %v", lines)
	}
	// The thread fetch keyed on the ROOT ts, not the reply's.
	repliesCalls := f.server.CallsFor("conversations.replies")
	last := repliesCalls[len(repliesCalls)-1]
	if last.Params.Get("ts") != "1770165000.000001" {
		t.Errorf("thread fetched with ts = %q", last.Params.Get("ts"))
	}
}

func TestChannelInviteExternal(t *testing.T) {
	f := newCLIFixture(t)
	f.server.Handle("conversations.inviteShared",
		mockslack.Response{Body: map[string]any{"ok": true}},
		mockslack.Response{Body: map[string]any{"ok": false, "error": "already_invited"}},
	)

	out, _, err := f.run(t, "channel", "invite", "--channel", "C12345678",
		"--users", "a@x.com, b@x.com ,U99999999", "--external", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if invited := payload["invited_emails"].([]any); len(invited) != 1 || invited[0] != "a@x.com" {
		t.Errorf("invited = %v", payload)
	}
	if already := payload["already_invited_emails"].([]any); len(already) != 1 || already[0] != "b@x.com" {
		t.Errorf("already = %v", payload)
	}
	if bad := payload["invalid_external_targets"].([]any); len(bad) != 1 || bad[0] != "U99999999" {
		t.Errorf("invalid = %v", payload)
	}
	if got := f.server.CallsFor("conversations.inviteShared")[0].Params.Get("external_limited"); got != "true" {
		t.Errorf("external_limited = %q", got)
	}
}

func TestChannelInviteExternalHardErrorAborts(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.inviteShared", map[string]any{"ok": false, "error": "not_allowed_token_type"})

	_, stderr, err := f.run(t, "channel", "invite", "--channel", "C12345678",
		"--users", "a@x.com,b@x.com", "--external", "--yes")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(errPayload(t, stderr)["error"].(string), "not_allowed_token_type") {
		t.Errorf("stderr = %s", stderr)
	}
	// Aborted after the first hard failure — no second invite attempted.
	if calls := len(f.server.CallsFor("conversations.inviteShared")); calls != 1 {
		t.Errorf("invite calls = %d, want 1", calls)
	}
}

func TestWorkflowRunFieldValidationBlocksSubmit(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok": true,
		"triggers": []any{map[string]any{
			"id": "Ft0001", "workflow": map[string]any{"workflow_id": "Wf001"},
		}},
	})
	f.server.HandleBody("workflows.get", map[string]any{
		"ok": true,
		"workflow": map[string]any{
			"id": "Wf001", "title": "Request",
			"steps": []any{map[string]any{
				"function": map[string]any{"callback_id": "open_form"},
				"inputs": map[string]any{
					"fields": map[string]any{"value": map[string]any{
						"elements": []any{map[string]any{"name": "f1", "title": "Summary", "type": "string"}},
						"required": []any{"f1"},
					}},
				},
			}},
		},
	})

	_, stderr, err := f.run(t, "workflow", "run", "Ft0001", "--channel", "C12345678", "--field", "Nope=x")
	if err == nil {
		t.Fatal("expected validation error")
	}
	payload := errPayload(t, stderr)
	if !strings.Contains(payload["error"].(string), `unknown field "Nope"`) {
		t.Errorf("payload = %v", payload)
	}
	// Validation failed → nothing tripped, nothing submitted.
	for _, method := range []string{"workflows.triggers.trip", "views.submit", "rtm.connect"} {
		if calls := len(f.server.CallsFor(method)); calls != 0 {
			t.Errorf("%s called %d times despite validation failure", method, calls)
		}
	}
}

func TestUnreadsDMUsesCounterpartName(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("client.counts", map[string]any{
		"ok":       true,
		"channels": []any{},
		"ims": []any{map[string]any{
			"id": "D1", "has_unreads": true, "unread_count_display": float64(1),
			"last_read": "1770165000.000000",
		}},
	})
	f.server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "D1", "is_im": true, "user": "U7"},
	})
	f.server.HandleBody("users.info", map[string]any{
		"ok": true, "user": map[string]any{"id": "U7", "profile": map[string]any{"display_name": "carol"}},
	})
	f.server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1770165001.000001", "U7", "dm ping"),
	))

	out, _, err := f.run(t, "unreads")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if lines[0]["channel_name"] != "carol" || lines[0]["channel_type"] != "dm" {
		t.Errorf("dm channel = %v", lines[0])
	}
}

func TestFormatYAMLContract(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul"})
	out, _, err := f.run(t, "auth", "test", "--format", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "user: paul") {
		t.Errorf("yaml output = %q", out)
	}
}

func TestBadFormatFailsBeforeMutation(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "delete",
		"https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "--yes", "--format", "bogus")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(errPayload(t, stderr)["error"].(string), "unknown format") {
		t.Errorf("stderr = %s", stderr)
	}
	if calls := len(f.server.Calls()); calls != 0 {
		t.Errorf("%d API calls happened despite the bad --format (mutate-then-fail)", calls)
	}
}
