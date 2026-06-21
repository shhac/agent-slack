package cli

import (
	"net/url"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestUserGetMultiple(t *testing.T) {
	f := newCLIFixture(t)
	for _, u := range []struct{ id, name string }{{"U0ALICEAA", "alice"}, {"U0BOBBBBB", "bob"}} {
		id, name := u.id, u.name
		f.server.HandleWhen("users.info",
			func(p url.Values) bool { return p.Get("user") == id },
			mockslack.Response{Body: mockslack.UserInfo(id, name)})
	}
	f.server.HandleWhen("users.info",
		func(p url.Values) bool { return p.Get("user") == "U0NOBODYZ" },
		mockslack.Response{Body: map[string]any{"ok": false, "error": "user_not_found"}})

	// Several args → NDJSON in input order: resolved records interleaved with
	// {"@unresolved":{id,reason,fixable_by}} for misses. Exit 0 on item-level misses.
	out, _, err := f.run(t, "user", "get", "U0ALICEAA", "U0BOBBBBB", "U0NOBODYZ")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 3 {
		t.Fatalf("want alice, bob, @unresolved — got %d lines: %v", len(lines), lines)
	}
	if lines[0]["id"] != "U0ALICEAA" || lines[1]["id"] != "U0BOBBBBB" {
		t.Errorf("users = %v", lines[:2])
	}
	un, ok := lines[2]["@unresolved"].(map[string]any)
	if !ok || un["id"] != "U0NOBODYZ" || un["fixable_by"] != "agent" {
		t.Errorf("@unresolved = %v", lines[2])
	}
}

// A sole miss emits one @unresolved record on stdout and exits 0 — item-level
// misses are not command failures under the EntityGet contract.
func TestUserGetSingleUnresolved(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.info", map[string]any{"ok": false, "error": "user_not_found"})
	out, _, err := f.run(t, "user", "get", "U0NOBODYZ")
	if err != nil {
		t.Fatalf("sole miss should exit 0; err=%v", err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 {
		t.Fatalf("want one @unresolved line, got %d: %v", len(lines), lines)
	}
	un, ok := lines[0]["@unresolved"].(map[string]any)
	if !ok || un["id"] != "U0NOBODYZ" || un["fixable_by"] != "agent" {
		t.Errorf("@unresolved = %v", lines[0])
	}
}

// --format json wraps the result in one envelope: resolved records in "data",
// item-level misses as structured objects under "@unresolved".
func TestUserGetMultipleJSONEnvelope(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleWhen("users.info", func(p url.Values) bool { return p.Get("user") == "U0ALICEAA" },
		mockslack.Response{Body: mockslack.UserInfo("U0ALICEAA", "alice")})
	f.server.HandleWhen("users.info", func(p url.Values) bool { return p.Get("user") == "U0NOBODYZ" },
		mockslack.Response{Body: map[string]any{"ok": false, "error": "user_not_found"}})

	out, _, err := f.run(t, "user", "get", "U0ALICEAA", "U0NOBODYZ", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if data, _ := payload["data"].([]any); len(data) != 1 {
		t.Errorf("data envelope = %v", payload["data"])
	}
	un, _ := payload["@unresolved"].([]any)
	if len(un) != 1 {
		t.Errorf("@unresolved in envelope = %v", payload["@unresolved"])
	}
	rec, _ := un[0].(map[string]any)
	if rec["id"] != "U0NOBODYZ" || rec["fixable_by"] != "agent" {
		t.Errorf("@unresolved[0] = %v", rec)
	}
}

// When every input fails, each produces its own @unresolved record in input order.
func TestUserGetAllUnresolved(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.info", map[string]any{"ok": false, "error": "user_not_found"})
	out, _, err := f.run(t, "user", "get", "U0NOBODYZ", "U0ALSOBADX")
	if err != nil {
		t.Fatalf("all-miss should exit 0; err=%v", err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 {
		t.Fatalf("want 2 @unresolved lines (one per input), got %d: %v", len(lines), lines)
	}
	for i, want := range []string{"U0NOBODYZ", "U0ALSOBADX"} {
		un, ok := lines[i]["@unresolved"].(map[string]any)
		if !ok || un["id"] != want || un["fixable_by"] != "agent" {
			t.Errorf("line %d @unresolved = %v", i, lines[i])
		}
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
	// Single arg → one NDJSON line (EntityGet default).
	lines := parseNDJSON(t, out)
	if len(lines) != 1 || lines[0]["email"] != "carol@acme.com" {
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
