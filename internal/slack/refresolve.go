package slack

import (
	"context"

	"github.com/shhac/agent-slack/internal/render"
)

// ResolvePolicy controls how referenced-entity resolution treats the cache.
type ResolvePolicy int

const (
	ResolveOff            ResolvePolicy = iota // don't resolve at all (zero value)
	ResolveCacheOnly                           // read cache; never fetch a miss
	ResolveCacheThenFetch                      // read cache, fetch misses UNLESS the category is complete
	ResolveBypassCache                         // ignore cached reads; refetch
)

// wantFetch reports whether a miss should be fetched under the policy, given
// whether the category was fully warmed within its completeness window (a
// complete category makes a miss authoritative, so the fetch is skipped).
func (p ResolvePolicy) wantFetch(complete bool) bool {
	switch p {
	case ResolveBypassCache:
		return true
	case ResolveCacheThenFetch:
		return !complete
	default: // ResolveCacheOnly
		return false
	}
}

// ReferencedEntities is the resolved form of the users/channels/usergroups a set
// of messages references, plus which categories required an API fetch (for the
// cache-warm hint). Users is already in referenced_users shape.
type ReferencedEntities struct {
	Users      map[string]CompactUser
	Channels   map[string]CompactChannel
	Usergroups map[string]CompactUsergroup
	Fetched    []string
}

// ResolveReferenced resolves every referenced user, channel, and usergroup under
// one policy — the single orchestration shared by message reads and search.
func ResolveReferenced(ctx context.Context, c *Client, refs render.ReferencedIDs, policy ResolvePolicy) ReferencedEntities {
	if policy == ResolveOff {
		return ReferencedEntities{}
	}
	users, uf := ResolveUsersByID(ctx, c, refs.Users, policy)
	channels, cf := ResolveChannelsByID(ctx, c, refs.Channels, policy)
	usergroups, gf := ResolveUsergroupsByID(ctx, c, refs.Usergroups, policy)
	ent := ReferencedEntities{
		Users:      ToReferencedUsers(refs.Users, users),
		Channels:   channels,
		Usergroups: usergroups,
	}
	for _, f := range []struct {
		ok  bool
		cat string
	}{{uf, "users"}, {cf, "channels"}, {gf, "usergroups"}} {
		if f.ok {
			ent.Fetched = append(ent.Fetched, f.cat)
		}
	}
	return ent
}

// Referenced-entity resolution: a rich_text mention carries only the bare id, so
// expanding <@U…>/<#C…>/<!subteam^S…> mentions to legible names means resolving
// each id. ResolveUsersByID (usercache.go) is the user case; these are the
// channel and usergroup analogs. Each returns whether it made an API fetch (so
// the CLI can hint toward `cache warm`); unresolved ids are omitted.

// ResolveChannelsByID expands channel ids to compact channels, reading the
// per-workspace resolution cache and fetching misses (conversations.info) per
// the policy.
func ResolveChannelsByID(ctx context.Context, c *Client, channelIDs []string, policy ResolvePolicy) (map[string]CompactChannel, bool) {
	out := map[string]CompactChannel{}
	snap := c.channelCache()
	fetchMiss := policy.wantFetch(c.channelsComplete())
	fetched := false
	for _, id := range channelIDs {
		if !render.IsReferencedChannelID(id) {
			continue
		}
		if _, done := out[id]; done {
			continue
		}
		if policy != ResolveBypassCache {
			if ch, ok := snap.get(id); ok {
				out[id] = ch
				continue
			}
		}
		if !fetchMiss {
			continue
		}
		resp, err := c.API(ctx, "conversations.info", map[string]any{"channel": id})
		if err != nil {
			continue
		}
		if ch := ToCompactChannel(getRec(resp, "channel")); ch.ID != "" {
			snap.set(ch.ID, ch)
			out[id] = ch
			fetched = true
		}
	}
	snap.save()
	if len(out) == 0 {
		return nil, fetched
	}
	return out, fetched
}

// ResolveUsergroupsByID expands usergroup (subteam) ids to compact usergroups.
// Usergroups are workspace-global and enumerated as one list, so a cache miss is
// resolved by one usergroups.list fetch (covering every wanted id at once).
func ResolveUsergroupsByID(ctx context.Context, c *Client, usergroupIDs []string, policy ResolvePolicy) (map[string]CompactUsergroup, bool) {
	want := map[string]bool{}
	for _, id := range usergroupIDs {
		if render.IsReferencedUsergroupID(id) {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return nil, false
	}
	out := map[string]CompactUsergroup{}

	if !policy.wantFetch(c.usergroupsComplete()) {
		snap := openCacheFor[CompactUsergroup](c, "usergroup-entities", cacheTTLOf(c.cache).Get, validUsergroup)
		for id := range want {
			if g, ok := snap.get(id); ok {
				out[id] = g
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, false
	}

	groups, err := fetchUsergroups(ctx, c, true)
	if err != nil {
		return nil, true
	}
	for _, g := range groups {
		if want[g.ID] {
			out[g.ID] = g
		}
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, true
}
