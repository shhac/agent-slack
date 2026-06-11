package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func userInfoBody(id, name string) map[string]any {
	return map[string]any{"ok": true, "user": map[string]any{"id": id, "name": name}}
}

func TestResolveUsersByIDFetchesAndCaches(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", userInfoBody("U12345678", "alice"))
	c := newStandardClient(t, server)
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	got := ResolveUsersByID(context.Background(), c, "https://acme.slack.com", []string{"U12345678"},
		ResolveUsersOptions{CacheDir: dir, Now: now})
	if got["U12345678"].Name != "alice" {
		t.Fatalf("got %+v", got)
	}
	if calls := len(server.CallsFor("users.info")); calls != 1 {
		t.Fatalf("API calls = %d", calls)
	}

	// Within the TTL the disk cache answers — no further API calls.
	later := now.Add(23 * time.Hour)
	got = ResolveUsersByID(context.Background(), c, "https://acme.slack.com", []string{"U12345678"},
		ResolveUsersOptions{CacheDir: dir, Now: later})
	if got["U12345678"].Name != "alice" {
		t.Fatalf("cache miss: %+v", got)
	}
	if calls := len(server.CallsFor("users.info")); calls != 1 {
		t.Errorf("API calls = %d, want still 1 (served from cache)", calls)
	}

	// Past the TTL it refetches. (Reset first: the original sticky fixture
	// would otherwise still be at the head of the queue.)
	server.Reset()
	server.HandleBody("users.info", userInfoBody("U12345678", "alice-renamed"))
	expired := now.Add(25 * time.Hour)
	got = ResolveUsersByID(context.Background(), c, "https://acme.slack.com", []string{"U12345678"},
		ResolveUsersOptions{CacheDir: dir, Now: expired})
	if got["U12345678"].Name != "alice-renamed" {
		t.Errorf("got %+v, want refetched user", got)
	}
}

func TestResolveUsersByIDForceRefresh(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", userInfoBody("U12345678", "v1"))
	c := newStandardClient(t, server)
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	opts := ResolveUsersOptions{CacheDir: dir, Now: now}
	ResolveUsersByID(context.Background(), c, "https://acme.slack.com", []string{"U12345678"}, opts)

	server.Reset()
	server.HandleBody("users.info", userInfoBody("U12345678", "v2"))
	opts.ForceRefresh = true
	got := ResolveUsersByID(context.Background(), c, "https://acme.slack.com", []string{"U12345678"}, opts)
	if got["U12345678"].Name != "v2" {
		t.Errorf("got %+v, want refetched despite fresh cache", got)
	}
}

func TestResolveUsersByIDBestEffort(t *testing.T) {
	server := mockslack.New()
	server.Handle("users.info",
		mockslack.Response{Body: map[string]any{"ok": false, "error": "user_not_found"}},
	)
	c := newStandardClient(t, server)

	got := ResolveUsersByID(context.Background(), c, "https://acme.slack.com",
		[]string{"U404NOTFOUND"}, ResolveUsersOptions{})
	if len(got) != 0 {
		t.Errorf("got %+v, want empty (failed fetches are skipped)", got)
	}
}

func TestResolveUsersByIDFiltersInvalidIDs(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "x"}) // must not hit the API
	got := ResolveUsersByID(context.Background(), c, "https://acme.slack.com",
		[]string{"", "not-an-id", "C12345678"}, ResolveUsersOptions{})
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestResolveUsersByIDSeparateWorkspaceCaches(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", userInfoBody("U12345678", "alice"))
	c := newStandardClient(t, server)
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	ResolveUsersByID(context.Background(), c, "https://one.slack.com", []string{"U12345678"},
		ResolveUsersOptions{CacheDir: dir, Now: now})
	// A different workspace must not see the first workspace's cache.
	server.HandleBody("users.info", userInfoBody("U12345678", "alice"))
	ResolveUsersByID(context.Background(), c, "https://two.slack.com", []string{"U12345678"},
		ResolveUsersOptions{CacheDir: dir, Now: now})

	if calls := len(server.CallsFor("users.info")); calls != 2 {
		t.Errorf("API calls = %d, want 2 (per-workspace caches)", calls)
	}
}

func TestToReferencedUsers(t *testing.T) {
	users := map[string]CompactUser{"U12345678": {ID: "U12345678", Name: "alice"}}
	got := ToReferencedUsers([]string{"U12345678", "U99999999"}, users)
	if len(got) != 1 || got["U12345678"].Name != "alice" {
		t.Errorf("got %+v", got)
	}
	if ToReferencedUsers([]string{"U99999999"}, users) != nil {
		t.Error("want nil when nothing resolved")
	}
}
