package slack

import (
	"context"

	"github.com/shhac/agent-slack/internal/render"
)

// Referenced-entity resolution: a rich_text mention carries only the bare id, so
// expanding <@U…>/<#C…>/<!subteam^S…> mentions to legible names means resolving
// each id. ResolveUsersByID (usercache.go) is the user case; these are the
// channel and usergroup analogs. forceRefresh bypasses cached reads; ids that
// don't resolve are omitted.

// ResolveChannelsByID expands channel ids to compact channels, reading the
// per-workspace resolution cache and fetching misses via conversations.info.
func ResolveChannelsByID(ctx context.Context, c *Client, channelIDs []string, forceRefresh bool) map[string]CompactChannel {
	out := map[string]CompactChannel{}
	snap := c.channelCache()
	for _, id := range channelIDs {
		if !render.IsReferencedChannelID(id) {
			continue
		}
		if _, done := out[id]; done {
			continue
		}
		if !forceRefresh {
			if ch, ok := snap.get(id); ok {
				out[id] = ch
				continue
			}
		}
		resp, err := c.API(ctx, "conversations.info", map[string]any{"channel": id})
		if err != nil {
			continue
		}
		ch := ToCompactChannel(getRec(resp, "channel"))
		if ch.ID != "" {
			snap.set(ch.ID, ch)
			out[id] = ch
		}
	}
	snap.save()
	if len(out) == 0 {
		return nil
	}
	return out
}

// ResolveUsergroupsByID expands usergroup (subteam) ids to compact usergroups.
// Usergroups are workspace-global and cached as one list, so a single
// (cached, or refetched when forceRefresh) enumeration covers every id.
func ResolveUsergroupsByID(ctx context.Context, c *Client, usergroupIDs []string, forceRefresh bool) map[string]CompactUsergroup {
	want := map[string]bool{}
	for _, id := range usergroupIDs {
		if render.IsReferencedUsergroupID(id) {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return nil
	}

	var groups []CompactUsergroup
	var err error
	if forceRefresh {
		groups, err = fetchUsergroups(ctx, c, true)
	} else {
		groups, err = ListUsergroups(ctx, c, ListUsergroupsOptions{IncludeDisabled: true})
	}
	if err != nil {
		return nil
	}

	out := map[string]CompactUsergroup{}
	for _, g := range groups {
		if want[g.ID] {
			out[g.ID] = g
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
