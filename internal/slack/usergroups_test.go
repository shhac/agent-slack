package slack

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// An S… id resolves directly, with no usergroups.list call.
func TestResolveUsergroupIDByID(t *testing.T) {
	server := mockslack.New() // no handlers: any API call would error
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	id, err := ResolveUsergroupID(context.Background(), c, "S0DIRECT01")
	if err != nil || id != "S0DIRECT01" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if n := len(server.CallsFor("usergroups.list")); n != 0 {
		t.Errorf("an S… id must not trigger usergroups.list, got %d", n)
	}
}

// A group with no handle is still resolvable by its S… id: the handle index
// skips it, so get falls through to the full list and matches on id.
func TestGetUsergroupByIDEmptyHandle(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{
		map[string]any{"id": "S0NOHANDLE", "name": "No Handle Group"}, // handle omitted
	}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	g, err := GetUsergroup(context.Background(), c, "S0NOHANDLE")
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != "S0NOHANDLE" || g.Name != "No Handle Group" {
		t.Errorf("group = %+v", g)
	}
}

// The IncludeDisabled branch: it sends the include_disabled param, projects
// date_delete into Disabled, and keys the page cache separately from the
// active-only set (so each is fetched independently).
func TestListUsergroupsIncludeDisabled(t *testing.T) {
	active := mockslack.Usergroup("S0ENGINEER", "eng", "Engineering")
	disabled := map[string]any{
		"id": "S0OLDGROUP", "handle": "old", "name": "Old Group",
		"date_delete": 1700000000, "prefs": map[string]any{},
	}
	server := mockslack.New()
	// include_disabled=true returns both; the default (param absent) returns only active.
	server.HandleWhen("usergroups.list",
		func(p url.Values) bool { return p.Get("include_disabled") == "true" },
		mockslack.Response{Body: mockslack.UsergroupsList(active, disabled)})
	server.HandleWhen("usergroups.list",
		func(p url.Values) bool { return p.Get("include_disabled") == "" },
		mockslack.Response{Body: mockslack.UsergroupsList(active)})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())
	ctx := context.Background()

	// Active-only first (no param) — one fetch, no disabled group.
	got, _, err := ListUsergroups(ctx, c, ListUsergroupsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "S0ENGINEER" {
		t.Fatalf("active-only = %+v, want just S0ENGINEER", got)
	}

	// Include-disabled keys a different page-cache entry → a second fetch that
	// sends include_disabled and surfaces the disabled group flagged.
	got, _, err = ListUsergroups(ctx, c, ListUsergroupsOptions{IncludeDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var old *CompactUsergroup
	for i := range got {
		if got[i].ID == "S0OLDGROUP" {
			old = &got[i]
		}
	}
	if old == nil || !old.Disabled {
		t.Fatalf("include-disabled = %+v, want S0OLDGROUP with Disabled=true", got)
	}

	calls := server.CallsFor("usergroups.list")
	if len(calls) != 2 {
		t.Fatalf("usergroups.list calls = %d, want 2 (distinct cache keys for disabled=false/true)", len(calls))
	}
	if calls[0].Params.Get("include_disabled") == "true" {
		t.Errorf("first (active) call must not send include_disabled")
	}
	if calls[1].Params.Get("include_disabled") != "true" {
		t.Errorf("second call must send include_disabled=true, got %v", calls[1].Params)
	}
}
