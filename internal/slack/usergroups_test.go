package slack

import (
	"context"
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
