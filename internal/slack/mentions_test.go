package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestResolveUsergroupID(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{
		map[string]any{"id": "S0YEET01", "handle": "yeeters", "name": "Yeeters"},
		map[string]any{"id": "S0MKT001", "handle": "marketing", "name": "Marketing"},
	}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	id, err := ResolveUsergroupID(context.Background(), c, "@yeeters")
	if err != nil || id != "S0YEET01" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	// The first call warmed every handle, so a different handle is a cache hit.
	if id, err := ResolveUsergroupID(context.Background(), c, "marketing"); err != nil || id != "S0MKT001" {
		t.Fatalf("marketing: id=%q err=%v", id, err)
	}
	if n := len(server.CallsFor("usergroups.list")); n != 1 {
		t.Errorf("usergroups.list called %d times, want 1 (cached)", n)
	}
}

func TestResolveUsergroupIDNotFoundNotCached(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{
		map[string]any{"id": "S0YEET01", "handle": "yeeters"},
	}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	for i := 0; i < 2; i++ {
		if id, err := ResolveUsergroupID(context.Background(), c, "ghost"); err != nil || id != "" {
			t.Fatalf("ghost lookup %d: id=%q err=%v", i, id, err)
		}
	}
	// A not-found must NOT be cached against the TTL (a group created later must
	// resolve), so the second lookup re-fetches.
	if n := len(server.CallsFor("usergroups.list")); n != 2 {
		t.Errorf("not-found should re-fetch: usergroups.list called %d times, want 2", n)
	}
}

func TestResolveMentionsUserBeatsUsergroup(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0DUP0001", "name": "dup"},
	}})
	server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{
		map[string]any{"id": "S0DUP0001", "handle": "dup"},
	}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	// A handle that is both a user and a usergroup resolves to the user.
	if got := ResolveMentions(context.Background(), c, "ping @dup"); got != "ping <@U0DUP0001>" {
		t.Errorf("user should win: got %q", got)
	}
}

func TestResolveMentions(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"},
	}})
	server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{
		map[string]any{"id": "S0YEET01", "handle": "yeeters"},
	}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	got := ResolveMentions(context.Background(), c,
		"hey @alice and @yeeters and @here and @U05BRPTKL6A and @nobody")
	want := "hey <@U0ALICEAA> and <!subteam^S0YEET01> and @here and @U05BRPTKL6A and @nobody"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestResolveMentionsSkipsCode(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"},
	}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	// @alice resolves in prose but stays literal inside `code` and ``` fences.
	in := "ping @alice but `not @alice here` and\n```\nrun @alice\n```\n"
	want := "ping <@U0ALICEAA> but `not @alice here` and\n```\nrun @alice\n```\n"
	if got := ResolveMentions(context.Background(), c, in); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestResolveMentionsNoCandidates(t *testing.T) {
	server := mockslack.New()
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	// No bare @handle → no API calls, text unchanged.
	in := "just a message with <@U12345678> already resolved and an a@b.com email"
	if got := ResolveMentions(context.Background(), c, in); got != in {
		t.Errorf("got %q", got)
	}
	if len(server.Calls()) != 0 {
		t.Errorf("expected no API calls, got %d", len(server.Calls()))
	}
}
