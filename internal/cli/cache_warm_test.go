package cli

import (
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

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
