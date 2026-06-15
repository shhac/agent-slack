package slack

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// Warming user profiles also fills the handles index, so a later @handle
// resolution is a cache hit instead of paginating users.list.
func TestWarmUserCacheFillsHandleIndex(t *testing.T) {
	server := mockslack.New() // no users.list handler: a fall-through scan would error
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	c.warmUserCache([]CompactUser{{ID: "U0ALICEAA", Name: "alice"}, {ID: "U0BOBBBBB", Name: "bob"}}, false)

	id, err := ResolveUserID(context.Background(), c, "@alice")
	if err != nil || id != "U0ALICEAA" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if n := len(server.CallsFor("users.list")); n != 0 {
		t.Errorf("users.list called %d times; warm should have filled the handles index", n)
	}
}

func userInfoBody(id, name string) map[string]any {
	return mockslack.UserInfo(id, name)
}

// TestResolveUsersByIDConcurrentFanout drives more distinct missing IDs than
// fetchConcurrency, so the fan-out goroutines write the shared result map in
// parallel. Run under -race, this validates the mutex/semaphore guarding; the
// count assertion catches lost updates even without the detector.
func TestResolveUsersByIDConcurrentFanout(t *testing.T) {
	server := mockslack.New()
	const n = 12 // > fetchConcurrency (5)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("U%08d", i)
		ids = append(ids, id)
		want := id
		server.HandleWhen("users.info",
			func(p url.Values) bool { return p.Get("user") == want },
			mockslack.Response{Body: userInfoBody(want, "user-"+want)},
		)
	}

	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())
	got := ResolveUsersByID(context.Background(), c, ids, false)

	if len(got) != n {
		t.Fatalf("got %d users, want %d (concurrent fan-out lost entries)", len(got), n)
	}
	for _, id := range ids {
		if got[id].ID != id {
			t.Errorf("missing or wrong entry for %s: %+v", id, got[id])
		}
	}
}

// cachingClient builds a standard-auth client whose workspace URL and cache
// are set, so ResolveUsersByID exercises the on-disk cache.
func cachingClient(t *testing.T, server *mockslack.Server, workspaceURL, dir string, mode CacheMode, now time.Time) *Client {
	t.Helper()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	cache := NewCache(dir, mode, DefaultCacheTTL(), func() time.Time { return now })
	return New(Auth{Type: AuthStandard, Token: "xoxb-test", WorkspaceURL: workspaceURL},
		WithBaseURL(ts.URL), WithCache(cache))
}

func TestResolveUsersByIDFetchesAndCaches(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", userInfoBody("U12345678", "alice"))
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	got := ResolveUsersByID(context.Background(), c, []string{"U12345678"}, false)
	if got["U12345678"].Name != "alice" {
		t.Fatalf("got %+v", got)
	}
	if calls := len(server.CallsFor("users.info")); calls != 1 {
		t.Fatalf("API calls = %d", calls)
	}

	// Within the TTL a fresh client serving the same workspace+dir hits cache.
	later := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now.Add(23*time.Hour))
	got = ResolveUsersByID(context.Background(), later, []string{"U12345678"}, false)
	if got["U12345678"].Name != "alice" {
		t.Fatalf("cache miss: %+v", got)
	}
	if calls := len(server.CallsFor("users.info")); calls != 1 {
		t.Errorf("API calls = %d, want still 1 (served from cache)", calls)
	}

	// Past the TTL it refetches.
	server.Reset()
	server.HandleBody("users.info", userInfoBody("U12345678", "alice-renamed"))
	expired := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now.Add(25*time.Hour))
	got = ResolveUsersByID(context.Background(), expired, []string{"U12345678"}, false)
	if got["U12345678"].Name != "alice-renamed" {
		t.Errorf("got %+v, want refetched user", got)
	}
}

func TestResolveUsersByIDForceRefresh(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", userInfoBody("U12345678", "v1"))
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	ResolveUsersByID(context.Background(), c, []string{"U12345678"}, false)

	server.Reset()
	server.HandleBody("users.info", userInfoBody("U12345678", "v2"))
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	got := ResolveUsersByID(context.Background(), c2, []string{"U12345678"}, true) // forceRefresh
	if got["U12345678"].Name != "v2" {
		t.Errorf("got %+v, want refetched despite fresh cache", got)
	}
}

func TestResolveUsersByIDRefreshModeWritesButSkipsReads(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", userInfoBody("U12345678", "v1"))
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	seed := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	ResolveUsersByID(context.Background(), seed, []string{"U12345678"}, false)

	server.Reset()
	server.HandleBody("users.info", userInfoBody("U12345678", "v2"))
	// CacheRefresh: ignore the cached v1, refetch v2, write it back.
	refresh := cachingClient(t, server, "https://acme.slack.com", dir, CacheRefresh, now)
	got := ResolveUsersByID(context.Background(), refresh, []string{"U12345678"}, false)
	if got["U12345678"].Name != "v2" {
		t.Errorf("refresh mode should skip the cached read: %+v", got)
	}

	// The refetch was written, so a Normal client now reads v2 without an API call.
	server.Reset()
	normal := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	got = ResolveUsersByID(context.Background(), normal, []string{"U12345678"}, false)
	if got["U12345678"].Name != "v2" || len(server.CallsFor("users.info")) != 0 {
		t.Errorf("refresh mode should have written: %+v, calls=%d", got, len(server.CallsFor("users.info")))
	}
}

func TestResolveUsersByIDNoCacheMode(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", userInfoBody("U12345678", "v1"))
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	off := cachingClient(t, server, "https://acme.slack.com", dir, CacheOff, now)
	ResolveUsersByID(context.Background(), off, []string{"U12345678"}, false)

	// Nothing was written: a Normal client must hit the API again.
	server.Reset()
	server.HandleBody("users.info", userInfoBody("U12345678", "v1"))
	normal := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	ResolveUsersByID(context.Background(), normal, []string{"U12345678"}, false)
	if calls := len(server.CallsFor("users.info")); calls != 1 {
		t.Errorf("no-cache mode must not persist; calls after = %d", calls)
	}
}

func TestResolveUsersByIDBestEffort(t *testing.T) {
	server := mockslack.New()
	server.Handle("users.info",
		mockslack.Response{Body: map[string]any{"ok": false, "error": "user_not_found"}},
	)
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	got := ResolveUsersByID(context.Background(), c, []string{"U404NOTFOUND"}, false)
	if len(got) != 0 {
		t.Errorf("got %+v, want empty (failed fetches are skipped)", got)
	}
}

func TestResolveUsersByIDFiltersInvalidIDs(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "x"}) // must not hit the API
	got := ResolveUsersByID(context.Background(), c, []string{"", "not-an-id", "C12345678"}, false)
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestResolveUsersByIDSeparateWorkspaceCaches(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.info", userInfoBody("U12345678", "alice"))
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	one := cachingClient(t, server, "https://one.slack.com", dir, CacheNormal, now)
	ResolveUsersByID(context.Background(), one, []string{"U12345678"}, false)
	// A different workspace must not see the first workspace's cache.
	server.HandleBody("users.info", userInfoBody("U12345678", "alice"))
	two := cachingClient(t, server, "https://two.slack.com", dir, CacheNormal, now)
	ResolveUsersByID(context.Background(), two, []string{"U12345678"}, false)

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
