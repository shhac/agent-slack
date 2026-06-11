package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestResolveUserIDCachesHandle(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{
		"ok":      true,
		"members": []any{map[string]any{"id": "U12345678", "name": "bob"}},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got, err := ResolveUserID(context.Background(), c, "@Bob"); err != nil || got != "U12345678" {
		t.Fatalf("got %q, %v", got, err)
	}

	// A fresh client resolves the same handle (any case) from cache — no scan.
	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got, err := ResolveUserID(context.Background(), c2, "bob"); err != nil || got != "U12345678" {
		t.Errorf("cached handle: got %q, %v", got, err)
	}
	if calls := len(server.CallsFor("users.list")); calls != 0 {
		t.Errorf("expected handle served from cache, got %d users.list calls", calls)
	}
}

func TestResolveUserIDCachesEmail(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.lookupByEmail", map[string]any{
		"ok": true, "user": map[string]any{"id": "U87654321"},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got, err := ResolveUserID(context.Background(), c, "Bob@Example.com"); err != nil || got != "U87654321" {
		t.Fatalf("got %q, %v", got, err)
	}

	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if got, err := ResolveUserID(context.Background(), c2, "bob@example.com"); err != nil || got != "U87654321" {
		t.Errorf("cached email: got %q, %v", got, err)
	}
	if calls := len(server.CallsFor("users.lookupByEmail")); calls != 0 {
		t.Errorf("expected email served from cache, got %d lookup calls", calls)
	}
}
