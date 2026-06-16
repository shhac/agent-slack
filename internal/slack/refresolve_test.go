package slack

import (
	"context"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// auto + a complete channel category: a miss is authoritative, so no
// conversations.info fetch (the sentinel-skip the policy exists to guarantee).
func TestResolveChannelsByIDNoFetchWhenComplete(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.list", mockslack.ConversationsList(mockslack.Channel("C0GENERAL0", "general")))
	server.HandleBody("conversations.info", mockslack.ChannelInfo("C0MISSING1", "missing")) // would resolve IF fetched
	c := completenessClient(t, server, CacheNormal)

	if err := WarmWorkspace(context.Background(), c, WarmOptions{Categories: []string{WarmChannels}, PageDelay: 0}, nil); err != nil {
		t.Fatal(err)
	}
	if !c.channelsComplete() {
		t.Fatal("a full channel warm should mark channels complete")
	}
	got, fetched := ResolveChannelsByID(context.Background(), c, []string{"C0MISSING1"}, ResolveCacheThenFetch)
	if got != nil || fetched {
		t.Errorf("auto+complete should skip the fetch: got=%v fetched=%v", got, fetched)
	}
	if n := len(server.CallsFor("conversations.info")); n != 0 {
		t.Errorf("conversations.info called %d times; a complete-category miss must not fetch", n)
	}
}

// cache-only never fetches (even cold); bypass always does.
func TestResolveChannelsByIDPolicyFetchBoundary(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.info", mockslack.ChannelInfo("C0MISSING1", "missing"))
	c := completenessClient(t, server, CacheNormal)

	if got, fetched := ResolveChannelsByID(context.Background(), c, []string{"C0MISSING1"}, ResolveCacheOnly); got != nil || fetched {
		t.Errorf("cache-only should not fetch: got=%v fetched=%v", got, fetched)
	}
	if n := len(server.CallsFor("conversations.info")); n != 0 {
		t.Errorf("cache-only must not call conversations.info, got %d", n)
	}
	got, fetched := ResolveChannelsByID(context.Background(), c, []string{"C0MISSING1"}, ResolveBypassCache)
	if got["C0MISSING1"].ID != "C0MISSING1" || !fetched {
		t.Errorf("bypass should fetch and resolve: got=%v fetched=%v", got, fetched)
	}
}

// usergroups cache-only resolves a known id from the entity cache with no
// usergroups.list call, and returns nil for an unknown id.
func TestResolveUsergroupsByIDCacheOnly(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing")))
	c := completenessClient(t, server, CacheNormal)

	if err := WarmWorkspace(context.Background(), c, WarmOptions{Categories: []string{WarmUsergroups}, PageDelay: 0}, nil); err != nil {
		t.Fatal(err)
	}
	afterWarm := len(server.CallsFor("usergroups.list"))

	got, fetched := ResolveUsergroupsByID(context.Background(), c, []string{"S0MARKETIN"}, ResolveCacheOnly)
	if g := got["S0MARKETIN"]; g.Handle != "marketing" || fetched {
		t.Errorf("cache-only should resolve from cache without fetch: got=%v fetched=%v", got, fetched)
	}
	if n := len(server.CallsFor("usergroups.list")); n != afterWarm {
		t.Errorf("cache-only made %d extra usergroups.list calls", n-afterWarm)
	}
	if miss, _ := ResolveUsergroupsByID(context.Background(), c, []string{"S0UNKNOWN0"}, ResolveCacheOnly); miss != nil {
		t.Errorf("cache-only miss should be nil, got %v", miss)
	}
}
