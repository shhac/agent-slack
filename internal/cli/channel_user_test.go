package cli

import (
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

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

func TestUserListWithDMAnnotations(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{
		"ok": true,
		"members": []any{
			map[string]any{"id": "U1", "name": "alice", "profile": map[string]any{"display_name": "Alice"}},
			map[string]any{"id": "U2", "name": "botty", "is_bot": true},
		},
	})
	f.server.HandleBody("conversations.list", map[string]any{
		"ok":       true,
		"channels": []any{map[string]any{"id": "D1", "user": "U1"}},
	})

	out, _, err := f.run(t, "user", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 { // bot filtered out by default
		t.Fatalf("lines = %v", lines)
	}
	if lines[0]["dm_id"] != "D1" || lines[0]["display_name"] != "Alice" {
		t.Errorf("user = %v", lines[0])
	}
}

func TestUserGetByHandle(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{
		"ok":      true,
		"members": []any{map[string]any{"id": "U7", "name": "carol"}},
	})
	f.server.HandleBody("users.info", map[string]any{
		"ok":   true,
		"user": map[string]any{"id": "U7", "name": "carol", "profile": map[string]any{"email": "carol@acme.com"}},
	})

	out, _, err := f.run(t, "user", "get", "@carol")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["email"] != "carol@acme.com" {
		t.Errorf("out = %s", out)
	}
}

func TestUserDMOpen(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.open", map[string]any{"ok": true, "channel": map[string]any{"id": "G555"}})
	out, _, err := f.run(t, "user", "dm-open", "U11111111", "U22222222")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["dm_channel_id"] != "G555" || payload["channel_type"] != "group_dm" {
		t.Errorf("payload = %v", payload)
	}
	if got := f.server.CallsFor("conversations.open")[0].Params.Get("users"); got != "U11111111,U22222222" {
		t.Errorf("users param = %q", got)
	}
}
