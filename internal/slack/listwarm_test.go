package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// The "show me what's here" commands must warm the completion/resolution
// caches, so a later complete or resolve needs no API call.

func TestListConversationsWarmsChannelCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.conversations", map[string]any{
		"ok": true,
		"channels": []any{
			map[string]any{"id": "C0AAAA1111", "name": "devs"},
			map[string]any{"id": "C0BBBB2222", "name": "design"},
		},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)

	if _, err := ListConversations(context.Background(), c, ConversationsOptions{}); err != nil {
		t.Fatal(err)
	}

	// Resolving a listed name is now free — no search.messages, no pagination.
	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if id, err := ResolveChannelID(context.Background(), c2, "design"); err != nil || id != "C0BBBB2222" {
		t.Errorf("resolve from warmed cache: got %q, %v", id, err)
	}
	if calls := len(server.Calls()); calls != 0 {
		t.Errorf("expected 0 API calls after warming, got %d", calls)
	}

	// And the channel is offered as a completion.
	items := ReadCompletions(dir, "https://acme.slack.com", "de", 10, CompleteChannels)
	if len(items) != 2 {
		t.Errorf("completions = %+v, want #devs and #design", items)
	}
}

func TestListUsersWarmsUserCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{
		"ok": true,
		"members": []any{
			map[string]any{"id": "U0AAAA1111", "name": "alice", "profile": map[string]any{"display_name": "Alice"}},
		},
	})
	server.HandleBody("conversations.list", map[string]any{"ok": true, "channels": []any{}}) // fetchDMMap
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)

	if _, err := ListUsers(context.Background(), c, ListUsersOptions{}); err != nil {
		t.Fatal(err)
	}

	// The profile is cached: a direct ID→profile resolve needs no users.info.
	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	got, _ := ResolveUsersByID(context.Background(), c2, []string{"U0AAAA1111"}, ResolveCacheThenFetch)
	if got["U0AAAA1111"].DisplayName != "Alice" {
		t.Errorf("resolve from warmed cache: %+v", got)
	}
	if calls := len(server.CallsFor("users.info")); calls != 0 {
		t.Errorf("expected 0 users.info after warming, got %d", calls)
	}
	if items := ReadCompletions(dir, "https://acme.slack.com", "", 10, CompleteUsers); len(items) != 1 {
		t.Errorf("user completions = %+v", items)
	}
}

func TestGetChannelInfoWarmsCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.info", map[string]any{
		"ok":      true,
		"channel": map[string]any{"id": "C0AAAA1111", "name": "devs", "num_members": float64(42)},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)

	compact, raw, err := GetChannelInfo(context.Background(), c, "C0AAAA1111")
	if err != nil || compact.Name != "devs" || compact.NumMembers != 42 {
		t.Fatalf("compact = %+v, err %v", compact, err)
	}
	if raw["num_members"].(float64) != 42 {
		t.Errorf("raw payload missing for --full: %v", raw)
	}
	// Grow-on-get: the name index is warmed, so a resolve is free.
	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if id, _ := ResolveChannelID(context.Background(), c2, "devs"); id != "C0AAAA1111" {
		t.Errorf("get did not warm the name index: %q", id)
	}
	if len(server.Calls()) != 0 {
		t.Errorf("expected 0 calls, got %d", len(server.Calls()))
	}
}

func TestListScheduledWarmsCompletionCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("chat.scheduledMessages.list", map[string]any{
		"ok": true,
		"scheduled_messages": []any{
			map[string]any{"id": "Q0AAAA1111", "channel_id": "C0AAAA1111", "post_at": float64(1800000000), "text": "stand-up reminder"},
		},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)

	if _, err := ListScheduledMessages(context.Background(), c, ScheduledListOptions{}); err != nil {
		t.Fatal(err)
	}

	// The scheduled id is now a completion candidate — no API call, just a cache read.
	items := ReadCompletions(dir, "https://acme.slack.com", "Q0", 10, CompleteScheduled)
	if len(items) != 1 || items[0].Value != "Q0AAAA1111" || items[0].Description != "stand-up reminder" {
		t.Errorf("scheduled completions = %+v", items)
	}
	// The command itself must never READ this cache (it always lists fresh).
	if got := ReadCompletions(dir, "https://acme.slack.com", "", 10, CompleteChannels|CompleteUsers); len(got) != 0 {
		t.Errorf("scheduled warm must not leak into channel/user completions: %+v", got)
	}
}

func TestListChannelMembers(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.members", map[string]any{
		"ok":                true,
		"members":           []any{"U0AAAA1111", "U0BBBB2222"},
		"response_metadata": map[string]any{"next_cursor": "page2"},
	})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())
	ids, next, err := ListChannelMembers(context.Background(), c, "C0AAAA1111", 100, "")
	if err != nil || len(ids) != 2 || ids[0] != "U0AAAA1111" || next != "page2" {
		t.Fatalf("ids=%v next=%q err=%v", ids, next, err)
	}
}
