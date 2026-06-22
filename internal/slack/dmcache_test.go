package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// The user→DM-channel mapping is permanent, so a second open of the same DM is
// served from the cache without re-hitting conversations.open.
func TestOpenDMChannelCachesMapping(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.open", map[string]any{"ok": true, "channel": map[string]any{"id": "D999"}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	for i := 0; i < 2; i++ {
		id, err := OpenDMChannel(context.Background(), c, "U0ALICEAA")
		if err != nil || id != "D999" {
			t.Fatalf("call %d: id=%q err=%v", i, id, err)
		}
	}
	if n := len(server.CallsFor("conversations.open")); n != 1 {
		t.Errorf("conversations.open called %d times; the mapping should be cached after the first open", n)
	}
}

// Warming dm-channels reads the already-open DM list and fills the cache, so a
// later open is a hit — and it must never call conversations.open (which would
// create a DM for a user we have none with).
func TestWarmDMChannelsFromExistingDMList(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.list", map[string]any{
		"ok": true, "channels": []any{
			map[string]any{"id": "D111", "user": "U0ALICEAA", "is_im": true},
			map[string]any{"id": "D222", "user": "U0BOBBBBB", "is_im": true},
		},
	})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	if err := WarmWorkspace(context.Background(), c, WarmOptions{Categories: []string{WarmDMChannels}}, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("conversations.open")); n != 0 {
		t.Fatalf("warm called conversations.open %d times; it must only read the existing-DM list", n)
	}
	id, err := OpenDMChannel(context.Background(), c, "U0ALICEAA")
	if err != nil || id != "D111" {
		t.Fatalf("id=%q err=%v; warm should have cached the DM id", id, err)
	}
	if n := len(server.CallsFor("conversations.open")); n != 0 {
		t.Errorf("open after warm hit the network (%d conversations.open); expected a cache hit", n)
	}
}

// Group DMs cache on the member set regardless of the order users were named,
// and the warm read still reports the right channel type.
func TestGroupDMCacheKeyIsOrderIndependent(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{
		"ok": true, "members": []any{
			map[string]any{"id": "U0ALICEAA", "name": "alice"},
			map[string]any{"id": "U0BOBBBBB", "name": "bob"},
		},
	})
	server.HandleBody("conversations.open", map[string]any{"ok": true, "channel": map[string]any{"id": "G555"}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	first, err := GetDMChannelForUsers(context.Background(), c, []string{"@alice", "@bob"})
	if err != nil || first.DMChannelID != "G555" || first.ChannelType != "group_dm" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := GetDMChannelForUsers(context.Background(), c, []string{"@bob", "@alice"})
	if err != nil || second.DMChannelID != "G555" || second.ChannelType != "group_dm" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if n := len(server.CallsFor("conversations.open")); n != 1 {
		t.Errorf("conversations.open called %d times; reversed member order should hit the same cache key", n)
	}
}
