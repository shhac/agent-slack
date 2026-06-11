package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestResolveChannelIDCachesName(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("search.messages", map[string]any{
		"ok":       true,
		"messages": map[string]any{"matches": []any{map[string]any{"channel": map[string]any{"id": "C0DEVS1234"}}}},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got, err := ResolveChannelID(context.Background(), c, "#devs"); err != nil || got != "C0DEVS1234" {
		t.Fatalf("got %q, %v", got, err)
	}
	if calls := len(server.CallsFor("search.messages")); calls != 1 {
		t.Fatalf("search calls = %d", calls)
	}

	// A fresh client over the same workspace+dir resolves the name from cache —
	// no search.messages, no conversations.list.
	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got, err := ResolveChannelID(context.Background(), c2, "devs"); err != nil || got != "C0DEVS1234" {
		t.Errorf("cached resolve: got %q, %v", got, err)
	}
	if calls := len(server.CallsFor("search.messages")) + len(server.CallsFor("conversations.list")); calls != 0 {
		t.Errorf("expected no name-resolution API calls, got %d", calls)
	}
}

func TestResolveChannelIDPaginationCachesPassedChannels(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("search.messages", map[string]any{"ok": false, "error": "not_allowed_token_type"})
	server.HandleBody("conversations.list", map[string]any{
		"ok": true,
		"channels": []any{
			map[string]any{"id": "C0AAA11111", "name": "random"},
			map[string]any{"id": "C0BBB22222", "name": "general"},
		},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got, _ := ResolveChannelID(context.Background(), c, "general"); got != "C0BBB22222" {
		t.Fatalf("got %q", got)
	}

	// "random" was paged past and cached opportunistically — resolving it now
	// needs no API call at all.
	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got, err := ResolveChannelID(context.Background(), c2, "random"); err != nil || got != "C0AAA11111" {
		t.Errorf("opportunistic cache: got %q, %v", got, err)
	}
	if calls := len(server.Calls()); calls != 0 {
		t.Errorf("expected 0 API calls for a previously-paged channel, got %d", calls)
	}
}

func TestResolveChannelNameCachesMeta(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C0DEVS1234", "name": "devs"},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got := ResolveChannelName(context.Background(), c, "C0DEVS1234"); got != "devs" {
		t.Fatalf("got %q", got)
	}

	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got := ResolveChannelName(context.Background(), c2, "C0DEVS1234"); got != "devs" {
		t.Errorf("cached name: got %q", got)
	}
	if calls := len(server.CallsFor("conversations.info")); calls != 0 {
		t.Errorf("expected name served from cache, got %d info calls", calls)
	}
}
