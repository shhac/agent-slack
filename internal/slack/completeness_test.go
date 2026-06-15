package slack

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// completenessClient is a fixed-clock client with the default (30m) completeness
// windows, in the given cache mode.
func completenessClient(t *testing.T, server *mockslack.Server, mode CacheMode) *Client {
	t.Helper()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	now := time.Now()
	cache := NewCache(t.TempDir(), mode, DefaultCacheTTL(), func() time.Time { return now })
	return New(Auth{Type: AuthStandard, Token: "xoxb-test", WorkspaceURL: "https://acme.slack.com"},
		WithBaseURL(ts.URL), WithCache(cache))
}

// The headline win: a batch of unknown @handle/@group mentions warms each
// category at most once, not once per miss.
func TestResolveMentionsBatchWarmsOnce(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0REALAAAA", "name": "real"},
	}})
	server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{
		map[string]any{"id": "S0REALGRP0", "handle": "realgroup"},
	}})
	c := completenessClient(t, server, CacheNormal)

	out := ResolveMentions(context.Background(), c, "ping @ghost1 @ghost2 @ghost3")
	if out != "ping @ghost1 @ghost2 @ghost3" {
		t.Errorf("unknown handles should stay literal, got %q", out)
	}
	if n := len(server.CallsFor("users.list")); n != 1 {
		t.Errorf("users.list called %d times for 3 misses, want 1 (warm-once)", n)
	}
	if n := len(server.CallsFor("usergroups.list")); n != 1 {
		t.Errorf("usergroups.list called %d times for 3 misses, want 1 (warm-once)", n)
	}
}

// A channel target: a stale miss searches + paginates once and marks the set
// complete; the next miss is authoritative (no search, no pagination).
func TestResolveChannelIDStaleThenAuthoritative(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("search.messages", mockslack.SearchMessages()) // no match
	server.HandleBody("conversations.list", mockslack.ConversationsList(mockslack.Channel("C0GENERAL0", "general")))
	c := completenessClient(t, server, CacheNormal)

	for i := 0; i < 2; i++ {
		if _, err := ResolveChannelID(context.Background(), c, "#ghost"); err == nil {
			t.Fatalf("lookup %d: expected not-found", i)
		}
	}
	if n := len(server.CallsFor("conversations.list")); n != 1 {
		t.Errorf("conversations.list called %d times, want 1 (2nd miss authoritative)", n)
	}
	if n := len(server.CallsFor("search.messages")); n != 1 {
		t.Errorf("search.messages called %d times, want 1 (sentinel skips search when complete)", n)
	}
}

// --refresh-cache (CacheRefresh) never trusts the sentinel, so each miss
// re-warms — an entity created after a warm still resolves.
func TestRefreshCacheBypassesSentinel(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{
		map[string]any{"id": "S0REALGRP0", "handle": "realgroup"},
	}})
	c := completenessClient(t, server, CacheRefresh)

	for i := 0; i < 2; i++ {
		if _, err := ResolveUsergroupID(context.Background(), c, "ghost"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(server.CallsFor("usergroups.list")); n != 2 {
		t.Errorf("refresh mode must not trust the sentinel: usergroups.list called %d times, want 2", n)
	}
}
