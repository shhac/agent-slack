package cli

import (
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// `cache warm users` warms only the users category — no channels/usergroups calls.
func TestCacheWarmScopedToUsers(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"},
	}})
	f.server.HandleBody("conversations.list", mockslack.ConversationsList())
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList())

	out, _, err := f.run(t, "cache", "warm", "users", "--page-delay", "0")
	if err != nil {
		t.Fatal(err)
	}
	cats := map[string]bool{}
	for _, line := range parseNDJSON(t, out) {
		cats[line["category"].(string)] = true
	}
	if !cats["users"] {
		t.Errorf("expected users to be warmed; events = %v", cats)
	}
	if cats["channels"] || cats["usergroups"] {
		t.Errorf("only users should be warmed; events = %v", cats)
	}
	// usergroups.list is the single-call category; it must not have been touched.
	if n := len(f.server.CallsFor("usergroups.list")); n != 0 {
		t.Errorf("usergroups.list called %d times; users-scoped warm must skip it", n)
	}
}

func TestCacheWarmRejectsUnknownCategory(t *testing.T) {
	f := newCLIFixture(t)
	if _, _, err := f.run(t, "cache", "warm", "bogus"); err == nil {
		t.Fatal("expected an error for an unknown category")
	}
}

func TestCacheWarm(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"},
		map[string]any{"id": "U0BOTABCD", "name": "botty", "is_bot": true},
	}})
	f.server.HandleBody("conversations.list", mockslack.ConversationsList(
		mockslack.Channel("C0GENERAL", "general"),
		mockslack.Channel("C0RANDOMM", "random"),
	))
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing", "C0GENERAL"),
	))

	out, _, err := f.run(t, "cache", "warm", "--page-delay", "0")
	if err != nil {
		t.Fatal(err)
	}

	done := map[string]int{}
	for _, line := range parseNDJSON(t, out) {
		if d, _ := line["done"].(bool); d {
			done[line["category"].(string)] = int(line["count"].(float64))
		}
	}
	if done["users"] != 1 { // the bot is filtered out by default
		t.Errorf("users warmed = %d, want 1", done["users"])
	}
	if done["channels"] != 2 {
		t.Errorf("channels warmed = %d, want 2", done["channels"])
	}
	if done["usergroups"] != 1 {
		t.Errorf("usergroups warmed = %d, want 1", done["usergroups"])
	}

	// The warm populated the usergroup handle index AND entity store: a
	// follow-up `usergroup get @marketing` now resolves and serves entirely
	// from cache, with no second usergroups.list call.
	if _, _, err := f.run(t, "usergroup", "get", "@marketing"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.server.CallsFor("usergroups.list")); n != 1 {
		t.Errorf("usergroups.list called %d times; warm should have made get a cache hit (want 1)", n)
	}
}

// --include-bots warms bot users too.
func TestCacheWarmIncludeBots(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"},
		map[string]any{"id": "U0BOTABCD", "name": "botty", "is_bot": true},
	}})
	f.server.HandleBody("conversations.list", mockslack.ConversationsList())
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList())

	out, _, err := f.run(t, "cache", "warm", "--page-delay", "0", "--include-bots")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range parseNDJSON(t, out) {
		if line["category"] == "users" {
			if d, _ := line["done"].(bool); d && int(line["count"].(float64)) != 2 {
				t.Errorf("users warmed = %v, want 2 with --include-bots", line["count"])
			}
		}
	}
}
