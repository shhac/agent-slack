package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

const rtWS = "https://acme.slack.com"

func TestChannelGetServesFromCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C0AAAA1111", "name": "devs", "num_members": float64(9)},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, rtWS, dir, CacheNormal, now)
	if _, _, err := GetChannelInfo(context.Background(), c, "C0AAAA1111"); err != nil {
		t.Fatal(err)
	}

	// Within the 5m Get window: served from cache, including the raw object
	// for --full — no second conversations.info.
	c2 := cachingClient(t, server, rtWS, dir, CacheNormal, now.Add(4*time.Minute))
	compact, raw, err := GetChannelInfo(context.Background(), c2, "C0AAAA1111")
	if err != nil || compact.NumMembers != 9 || raw["num_members"].(float64) != 9 {
		t.Fatalf("compact=%+v raw=%v err=%v", compact, raw, err)
	}
	if n := len(server.CallsFor("conversations.info")); n != 1 {
		t.Errorf("served from cache should be 1 call, got %d", n)
	}

	// Past the window: refetch.
	c3 := cachingClient(t, server, rtWS, dir, CacheNormal, now.Add(6*time.Minute))
	if _, _, err := GetChannelInfo(context.Background(), c3, "C0AAAA1111"); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("conversations.info")); n != 2 {
		t.Errorf("past the window should refetch, got %d calls", n)
	}
}

func TestChannelGetNoCacheMode(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.info", map[string]any{"ok": true, "channel": map[string]any{"id": "C0AAAA1111", "name": "devs"}})
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	off := cachingClient(t, server, rtWS, dir, CacheOff, now)
	GetChannelInfo(context.Background(), off, "C0AAAA1111")
	GetChannelInfo(context.Background(), off, "C0AAAA1111")
	if n := len(server.CallsFor("conversations.info")); n != 2 {
		t.Errorf("--no-cache must always fetch; got %d", n)
	}
}

func TestUserGetServesFromCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", mockslack.UserInfo("U0AAAA1111", "alice"))
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, rtWS, dir, CacheNormal, now)
	if _, err := GetUser(context.Background(), c, "U0AAAA1111"); err != nil {
		t.Fatal(err)
	}
	c2 := cachingClient(t, server, rtWS, dir, CacheNormal, now.Add(4*time.Minute))
	if u, err := GetUser(context.Background(), c2, "U0AAAA1111"); err != nil || u.Name != "alice" {
		t.Fatalf("u=%+v err=%v", u, err)
	}
	if n := len(server.CallsFor("users.info")); n != 1 {
		t.Errorf("served from cache should be 1 call, got %d", n)
	}
}

func TestListConversationsPageCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.conversations", map[string]any{
		"ok": true, "channels": []any{map[string]any{"id": "C0AAAA1111", "name": "devs"}},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	opts := ConversationsOptions{Limit: 100, Types: "public_channel"}

	c := cachingClient(t, server, rtWS, dir, CacheNormal, now)
	if _, err := ListConversations(context.Background(), c, opts); err != nil {
		t.Fatal(err)
	}
	// Same query within the window: served from the page cache.
	c2 := cachingClient(t, server, rtWS, dir, CacheNormal, now.Add(2*time.Minute))
	if _, err := ListConversations(context.Background(), c2, opts); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("users.conversations")); n != 1 {
		t.Errorf("repeat list should hit the page cache, got %d calls", n)
	}
	// A different query is a different page.
	if _, err := ListConversations(context.Background(), c2, ConversationsOptions{Limit: 50, Types: "public_channel"}); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("users.conversations")); n != 2 {
		t.Errorf("different query should fetch, got %d calls", n)
	}
}

func TestListUsersPageCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{map[string]any{"id": "U0AAAA1111", "name": "alice"}}})
	server.HandleBody("conversations.list", map[string]any{"ok": true, "channels": []any{}}) // fetchDMMap
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, rtWS, dir, CacheNormal, now)
	if _, err := ListUsers(context.Background(), c, ListUsersOptions{Limit: 50}); err != nil {
		t.Fatal(err)
	}
	c2 := cachingClient(t, server, rtWS, dir, CacheNormal, now.Add(2*time.Minute))
	if _, err := ListUsers(context.Background(), c2, ListUsersOptions{Limit: 50}); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("users.list")); n != 1 {
		t.Errorf("repeat user list should hit the page cache, got %d calls", n)
	}
}
