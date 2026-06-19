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
	f.server.HandleBody("emoji.list", map[string]any{"ok": true, "emoji": map[string]any{
		"partyparrot": "https://emoji.slack-edge.com/T1/partyparrot/abc.gif",
		"shipit":      "alias:partyparrot",
	}})

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
	if done["users"] != 2 { // bots are included by default (complete set arms the sentinel)
		t.Errorf("users warmed = %d, want 2 (alice + bot)", done["users"])
	}
	if done["channels"] != 2 {
		t.Errorf("channels warmed = %d, want 2", done["channels"])
	}
	if done["usergroups"] != 1 {
		t.Errorf("usergroups warmed = %d, want 1", done["usergroups"])
	}
	if done["emoji"] != 2 {
		t.Errorf("emoji warmed = %d, want 2", done["emoji"])
	}

	// The warm populated the emoji cache: a follow-up `emoji get` serves from
	// cache with no second emoji.list call.
	if _, _, err := f.run(t, "emoji", "get", "partyparrot"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.server.CallsFor("emoji.list")); n != 1 {
		t.Errorf("emoji.list called %d times; warm should have made get a cache hit (want 1)", n)
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

// --no-bots excludes bot users (opt out of the default complete warm).
func TestCacheWarmNoBots(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"},
		map[string]any{"id": "U0BOTABCD", "name": "botty", "is_bot": true},
	}})
	f.server.HandleBody("conversations.list", mockslack.ConversationsList())
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList())
	f.server.HandleBody("emoji.list", map[string]any{"ok": true, "emoji": map[string]any{}})

	out, _, err := f.run(t, "cache", "warm", "--page-delay", "0", "--no-bots")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range parseNDJSON(t, out) {
		if line["category"] == "users" {
			if d, _ := line["done"].(bool); d && int(line["count"].(float64)) != 1 {
				t.Errorf("users warmed = %v, want 1 with --no-bots", line["count"])
			}
		}
	}
}

// --stale-only re-warms only categories whose completeness sentinel has lapsed.
// After a full warm arms all four, a second --stale-only run skips them all
// (emitting skipped events) and makes no new list calls.
func TestCacheWarmStaleOnly(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"},
	}})
	f.server.HandleBody("conversations.list", mockslack.ConversationsList(
		mockslack.Channel("C0GENERAL", "general"),
	))
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing"),
	))
	f.server.HandleBody("emoji.list", map[string]any{"ok": true, "emoji": map[string]any{
		"partyparrot": "https://emoji.slack-edge.com/T1/partyparrot/abc.gif",
	}})

	// First warm arms every sentinel.
	if _, _, err := f.run(t, "cache", "warm", "--page-delay", "0"); err != nil {
		t.Fatal(err)
	}
	before := map[string]int{
		"users.list":      len(f.server.CallsFor("users.list")),
		"conversations":   len(f.server.CallsFor("conversations.list")),
		"usergroups.list": len(f.server.CallsFor("usergroups.list")),
		"emoji.list":      len(f.server.CallsFor("emoji.list")),
	}

	out, _, err := f.run(t, "cache", "warm", "--page-delay", "0", "--stale-only")
	if err != nil {
		t.Fatal(err)
	}
	skipped := 0
	for _, line := range parseNDJSON(t, out) {
		if s, _ := line["skipped"].(bool); s {
			skipped++
		}
	}
	if skipped != 4 {
		t.Errorf("want 4 skipped categories, got %d: %s", skipped, out)
	}
	if n := len(f.server.CallsFor("users.list")); n != before["users.list"] {
		t.Errorf("--stale-only re-fetched users.list (%d → %d)", before["users.list"], n)
	}
	if n := len(f.server.CallsFor("conversations.list")); n != before["conversations"] {
		t.Errorf("--stale-only re-fetched conversations.list")
	}
	if n := len(f.server.CallsFor("usergroups.list")); n != before["usergroups.list"] {
		t.Errorf("--stale-only re-fetched usergroups.list")
	}
	if n := len(f.server.CallsFor("emoji.list")); n != before["emoji.list"] {
		t.Errorf("--stale-only re-fetched emoji.list")
	}
}
