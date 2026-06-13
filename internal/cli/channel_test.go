package cli

import (
	"net/url"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestChannelGetMultiple(t *testing.T) {
	f := newCLIFixture(t)
	for _, c := range []struct{ id, name string }{{"C0DEVSAAA", "devs"}, {"C0OPSAAAA", "ops"}} {
		id, name := c.id, c.name
		f.server.HandleWhen("conversations.info",
			func(p url.Values) bool { return p.Get("channel") == id },
			mockslack.Response{Body: map[string]any{"ok": true, "channel": map[string]any{"id": id, "name": name}}})
	}
	f.server.HandleWhen("conversations.info",
		func(p url.Values) bool { return p.Get("channel") == "C0GONEAAA" },
		mockslack.Response{Body: map[string]any{"ok": false, "error": "channel_not_found"}})

	out, _, err := f.run(t, "channel", "get", "C0DEVSAAA", "C0OPSAAAA", "C0GONEAAA")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 3 {
		t.Fatalf("want 2 channels + 1 meta line, got %d: %v", len(lines), lines)
	}
	if lines[0]["name"] != "devs" || lines[1]["name"] != "ops" {
		t.Errorf("channels = %v", lines[:2])
	}
	un, ok := lines[2]["@unresolved"].([]any)
	if !ok || len(un) != 1 || un[0] != "C0GONEAAA" {
		t.Errorf("@unresolved = %v", lines[2])
	}
}

func TestChannelListCompact(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.conversations", map[string]any{
		"ok": true,
		"channels": []any{
			map[string]any{
				"id": "C1", "name": "general", "is_member": true, "num_members": float64(42),
				"topic":      map[string]any{"value": "All things acme"},
				"purpose":    map[string]any{"value": "ignored in compact"},
				"properties": map[string]any{"huge": "blob"},
			},
		},
		"response_metadata": map[string]any{"next_cursor": "cur2"},
	})

	out, _, err := f.run(t, "channel", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 { // channel + @pagination
		t.Fatalf("lines = %d: %s", len(lines), out)
	}
	ch := lines[0]
	if ch["id"] != "C1" || ch["name"] != "general" || ch["topic"] != "All things acme" || ch["num_members"] != float64(42) {
		t.Errorf("channel = %v", ch)
	}
	if _, has := ch["properties"]; has {
		t.Error("compact projection should drop bulky fields")
	}
	pagination := lines[1]["@pagination"].(map[string]any)
	if pagination["next_cursor"] != "cur2" {
		t.Errorf("pagination = %v", pagination)
	}
}

func TestChannelGet(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.info", map[string]any{
		"ok":      true,
		"channel": map[string]any{"id": "C12345678", "name": "general", "num_members": float64(7), "is_private": false},
	})
	out, _, err := f.run(t, "channel", "get", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	ch := parseJSON(t, out)
	if ch["name"] != "general" || ch["num_members"] != float64(7) {
		t.Errorf("channel = %v", ch)
	}
	if got := f.server.CallsFor("conversations.info")[0].Params.Get("include_num_members"); got != "true" {
		t.Errorf("include_num_members = %q, want true", got)
	}
}

func TestChannelMembers(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.members", map[string]any{
		"ok": true, "members": []any{"U11111111", "U22222222"},
		"response_metadata": map[string]any{"next_cursor": "more"},
	})
	out, _, err := f.run(t, "channel", "members", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if lines[0]["id"] != "U11111111" || lines[1]["id"] != "U22222222" {
		t.Errorf("members = %v", lines)
	}
	var sawChannelMeta bool
	for _, l := range lines {
		if l["@channel_id"] == "C12345678" {
			sawChannelMeta = true
		}
	}
	if !sawChannelMeta {
		t.Errorf("missing @channel_id meta: %v", lines)
	}
}

func TestChannelListFullPassthrough(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.conversations", map[string]any{
		"ok":       true,
		"channels": []any{map[string]any{"id": "C1", "properties": map[string]any{"kept": true}}},
	})
	out, _, err := f.run(t, "channel", "list", "--full")
	if err != nil {
		t.Fatal(err)
	}
	if _, has := parseNDJSON(t, out)[0]["properties"]; !has {
		t.Error("--full should pass the raw payload through")
	}
}

func TestChannelListAllUsesConversationsList(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.list", map[string]any{"ok": true, "channels": []any{}})
	if _, _, err := f.run(t, "channel", "list", "--all"); err != nil {
		t.Fatal(err)
	}
	if len(f.server.CallsFor("conversations.list")) != 1 {
		t.Error("--all should call conversations.list")
	}
}

func TestChannelNewRequiresYes(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "channel", "new", "--name", "incident-123")
	if err == nil {
		t.Fatal("expected error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" || !strings.Contains(payload["error"].(string), "incident-123") {
		t.Errorf("payload = %v", payload)
	}

	f.server.HandleBody("conversations.create", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C9", "name": "incident-123", "is_private": false},
	})
	out, _, err := f.run(t, "channel", "new", "--name", "incident-123", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	created := parseJSON(t, out)["channel"].(map[string]any)
	if created["id"] != "C9" {
		t.Errorf("out = %s", out)
	}
}

func TestChannelInvite(t *testing.T) {
	f := newCLIFixture(t)
	f.server.Handle("conversations.invite",
		// first user succeeds, second is already in the channel
		mockslack.Response{Body: map[string]any{"ok": true}},
		mockslack.Response{Body: map[string]any{"ok": false, "error": "already_in_channel"}},
	)

	out, _, err := f.run(t, "channel", "invite", "--channel", "C12345678", "--users", "U11111111,U22222222", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	invited := payload["invited_user_ids"].([]any)
	already := payload["already_in_channel_user_ids"].([]any)
	if len(invited) != 1 || invited[0] != "U11111111" {
		t.Errorf("invited = %v", invited)
	}
	if len(already) != 1 || already[0] != "U22222222" {
		t.Errorf("already = %v", already)
	}
}

func TestChannelMark(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.mark", map[string]any{"ok": true})
	out, _, err := f.run(t, "channel", "mark", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["ts"] != "1770165109.628379" {
		t.Errorf("out = %s", out)
	}
	call := f.server.CallsFor("conversations.mark")[0]
	if call.Params.Get("channel") != "C1A2B3C4D" {
		t.Errorf("params = %v", call.Params)
	}
}
