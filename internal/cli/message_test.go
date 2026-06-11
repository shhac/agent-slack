package cli

import (
	"strings"
	"testing"
)

func TestAuthTest(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul", "team": "Acme", "user_id": "U12345678"})

	out, _, err := f.run(t, "auth", "test")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["user"] != "paul" || payload["auth_type"] != "standard" {
		t.Errorf("payload = %v", payload)
	}
	if got := f.server.CallsFor("auth.test")[0].Header.Get("Authorization"); got != "Bearer xoxb-test-token" {
		t.Errorf("authorization = %q", got)
	}
}

func TestMessageGetByPermalink(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.628379", "U12345678", "Hello <@U87654321> :rocket:"),
	))

	out, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C060RS20UMV/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	msg := payload["message"].(map[string]any)
	if msg["content"] != "Hello @U87654321 🚀" {
		t.Errorf("content = %q", msg["content"])
	}
	if msg["ts"] != "1770165109.628379" || msg["channel_id"] != "C060RS20UMV" {
		t.Errorf("msg = %v", msg)
	}
	if payload["permalink"] != "https://acme.slack.com/archives/C060RS20UMV/p1770165109628379" {
		t.Errorf("permalink = %v", payload["permalink"])
	}
	if _, hasThread := payload["thread"]; hasThread {
		t.Error("no thread expected for a plain message")
	}
}

func TestMessageGetThreadSummary(t *testing.T) {
	f := newCLIFixture(t)
	msg := simpleMessage("1770165109.628379", "U12345678", "root")
	msg["reply_count"] = 4
	f.server.HandleBody("conversations.history", historyWith(msg))

	out, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	thread := parseJSON(t, out)["thread"].(map[string]any)
	if thread["ts"] != "1770165109.628379" || thread["length"] != float64(5) {
		t.Errorf("thread = %v", thread)
	}
}

func TestMessageGetChannelRequiresTS(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "get", "#general")
	if err == nil {
		t.Fatal("expected error")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestMessageListChannelHistory(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165110.000002", "U2", "second"),
		simpleMessage("1770165109.000001", "U1", "first"),
	))

	out, _, err := f.run(t, "message", "list", "#general")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 3 { // 2 messages + @channel_id meta
		t.Fatalf("lines = %d: %s", len(lines), out)
	}
	// Chronological order.
	if lines[0]["content"] != "first" || lines[1]["content"] != "second" {
		t.Errorf("order wrong: %v", lines)
	}
	if lines[2]["@channel_id"] != "C123" {
		t.Errorf("meta = %v", lines[2])
	}
}

func TestMessageListThread(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	root := simpleMessage("1.000001", "U1", "root")
	root["thread_ts"] = "1.000001"
	root["reply_count"] = 1
	reply := simpleMessage("2.000002", "U2", "reply")
	reply["thread_ts"] = "1.000001"
	f.server.HandleBody("conversations.replies", map[string]any{
		"ok":       true,
		"messages": []any{root, reply},
	})

	out, _, err := f.run(t, "message", "list", "#general", "--thread-ts", "1.000001")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 4 { // 2 messages + @channel_id + @thread_ts
		t.Fatalf("lines = %d: %s", len(lines), out)
	}
	// channel_id/thread_ts stripped from rows (they're in the meta lines).
	if _, has := lines[0]["channel_id"]; has {
		t.Error("thread rows should not repeat channel_id")
	}
}

func TestMessageListReactionFiltersRequireOldest(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	_, stderr, err := f.run(t, "message", "list", "#general", "--with-reaction", "eyes")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(errPayload(t, stderr)["error"].(string), "--oldest") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestMessageSendToChannel(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1770165109.628379", "channel": "C123"})

	out, _, err := f.run(t, "message", "send", "#general", "@U05BRPTKL6A check this & that")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["ok"] != true || payload["ts"] != "1770165109.628379" {
		t.Errorf("payload = %v", payload)
	}
	if payload["permalink"] != "https://acme.slack.com/archives/C123/p1770165109628379" {
		t.Errorf("permalink = %v", payload["permalink"])
	}
	call := f.server.CallsFor("chat.postMessage")[0]
	if got := call.Params.Get("text"); got != "<@U05BRPTKL6A> check this &amp; that" {
		t.Errorf("text = %q (outbound formatting)", got)
	}
	if call.Params.Has("blocks") {
		t.Error("plain text should not send rich_text blocks")
	}
}

func TestMessageSendListBecomesRichText(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "C123"})

	if _, _, err := f.run(t, "message", "send", "#general", "Plan:\n- one\n- two"); err != nil {
		t.Fatal(err)
	}
	blocks := f.server.CallsFor("chat.postMessage")[0].Params.Get("blocks")
	if !strings.Contains(blocks, "rich_text_list") {
		t.Errorf("blocks = %q, want rich_text_list", blocks)
	}
}

func TestMessageSendDMTarget(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.open", map[string]any{"ok": true, "channel": map[string]any{"id": "D999"}})
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "D999"})

	out, _, err := f.run(t, "message", "send", "U12345ABCDE", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["channel_id"] != "D999" {
		t.Errorf("out = %s", out)
	}
}

func TestMessageSendScheduled(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.scheduleMessage", map[string]any{
		"ok": true, "channel": "C123", "scheduled_message_id": "Q123", "post_at": float64(9999999999),
	})

	out, _, err := f.run(t, "message", "send", "#general", "later", "--schedule-in", "3h")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["scheduled_message_id"] != "Q123" {
		t.Errorf("payload = %v", payload)
	}
	if len(f.server.CallsFor("chat.postMessage")) != 0 {
		t.Error("scheduled send must not call chat.postMessage")
	}
}

func TestMessageEditRequiresYes(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "edit", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "new text")
	if err == nil {
		t.Fatal("expected error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" {
		t.Errorf("payload = %v", payload)
	}
	if !strings.Contains(payload["hint"].(string), "--yes") {
		t.Errorf("hint = %v", payload["hint"])
	}
	if len(f.server.Calls()) != 0 {
		t.Error("no API calls expected without --yes")
	}
}

func TestMessageEditWithYes(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	_, _, err := f.run(t, "message", "edit", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "new *text*", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("chat.update")[0]
	if call.Params.Get("ts") != "1770165109.628379" || call.Params.Get("channel") != "C1A2B3C4D" {
		t.Errorf("params = %v", call.Params)
	}
}

func TestMessageDeleteWithYes(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.delete", map[string]any{"ok": true})

	_, _, err := f.run(t, "message", "delete", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.server.CallsFor("chat.delete")) != 1 {
		t.Error("chat.delete not called")
	}
}

func TestMessageReactAddUnicode(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("reactions.add", map[string]any{"ok": true})

	_, _, err := f.run(t, "message", "react", "add", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "🚀")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.server.CallsFor("reactions.add")[0].Params.Get("name"); got != "rocket" {
		t.Errorf("name = %q", got)
	}
}

func TestScheduledCancelRequiresYes(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "scheduled", "cancel", "Q123", "--channel", "C1A2B3C4D")
	if err == nil {
		t.Fatal("expected error")
	}
	if errPayload(t, stderr)["fixable_by"] != "human" {
		t.Errorf("stderr = %s", stderr)
	}

	f.server.HandleBody("chat.deleteScheduledMessage", map[string]any{"ok": true})
	out, _, err := f.run(t, "message", "scheduled", "cancel", "Q123", "--channel", "C1A2B3C4D", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["scheduled_message_id"] != "Q123" {
		t.Errorf("out = %s", out)
	}
}

func TestScheduledList(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.scheduledMessages.list", map[string]any{
		"ok":                 true,
		"scheduled_messages": []any{map[string]any{"id": "Q1", "post_at": float64(2000000000)}},
	})
	out, _, err := f.run(t, "message", "scheduled", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 || lines[0]["id"] != "Q1" {
		t.Errorf("lines = %v", lines)
	}
}
