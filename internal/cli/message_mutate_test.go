package cli

import (
	"strings"
	"testing"
)

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

func TestMessageEditDialectAndMentions(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"}}})
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	_, _, err := f.run(t, "message", "edit",
		"https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379",
		"hi @alice in **bold**", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("chat.update")[0]
	// Markdown formatting → rich_text blocks with the resolved mention.
	if blocks := call.Params.Get("blocks"); !strings.Contains(blocks, `"bold":true`) || !strings.Contains(blocks, `"user_id":"U0ALICEAA"`) {
		t.Errorf("edit blocks missing bold/mention: %s", blocks)
	}
	// The text fallback is de-marked plain, with the mention promoted.
	if got := call.Params.Get("text"); got != "hi <@U0ALICEAA> in bold" {
		t.Errorf("edit text fallback = %q", got)
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
