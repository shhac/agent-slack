package slack

import (
	"context"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// NormalizeChannelInput splits a channel argument into an ID or a name:
// "#general" and "general" yield name "general"; "C0123456789" yields an ID.
// Exactly one return value is non-empty (name may be "" for empty input).
func NormalizeChannelInput(input string) (id, name string) {
	trimmed := strings.TrimSpace(input)
	if rest, ok := strings.CutPrefix(trimmed, "#"); ok {
		return "", rest
	}
	if render.IsChannelID(trimmed) {
		return trimmed, ""
	}
	return "", trimmed
}

// ResolveChannelID turns "#name"/"name"/"C…" into a conversation ID.
//
// Slack has no name→ID lookup API. conversations.list paginates the entire
// workspace (200 at a time), which is O(channels) calls — minutes in large
// workspaces. search.messages with `in:#name` resolves it in one call by
// returning a message whose metadata carries the channel ID, so try that
// first and fall back to pagination (search may be unavailable to the token).
func ResolveChannelID(ctx context.Context, c *Client, input string) (string, error) {
	id, name := NormalizeChannelInput(input)
	if id != "" {
		return id, nil
	}
	if name == "" {
		return "", agenterrors.New("channel name is empty", agenterrors.FixableByAgent).
			WithHint("pass #name, a channel name, or a channel ID (C…)")
	}

	if id, ok := c.cachedChannelID(name); ok {
		return id, nil
	}

	if id := channelIDViaSearch(ctx, c, name); id != "" {
		c.cacheChannelID(name, id)
		return id, nil
	}

	found := ""
	err := EachPage(ctx, c, "conversations.list", map[string]any{
		"exclude_archived": true,
		"limit":            200,
		"types":            "public_channel,private_channel",
	}, func(resp map[string]any) (bool, error) {
		for _, ch := range recItems(getArr(resp, "channels")) {
			// Opportunistically cache every channel object we page past, so
			// later name/ID lookups skip this scan entirely.
			c.cacheChannel(ToCompactChannel(ch))
			if getStr(ch, "name") == name && getStr(ch, "id") != "" {
				found = getStr(ch, "id")
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "could not resolve channel name: #%s", name).
			WithHint("check the name or pass a channel ID (C…) — 'agent-slack channel list' shows conversations")
	}
	c.cacheChannelID(name, found)
	return found, nil
}

func channelIDViaSearch(ctx context.Context, c *Client, name string) string {
	resp, err := c.API(ctx, "search.messages", map[string]any{
		"query":    "in:#" + name,
		"count":    1,
		"sort":     "timestamp",
		"sort_dir": "desc",
	})
	if err != nil {
		return ""
	}
	matches := recItems(getArr(getRec(resp, "messages"), "matches"))
	if len(matches) == 0 {
		return ""
	}
	return getStr(getRec(matches[0], "channel"), "id")
}

// GetChannelInfo fetches one channel's metadata (conversations.info), warming
// the cache (so completions/resolvers grow from a direct get), and returns the
// compact projection plus the raw channel object for --full.
func GetChannelInfo(ctx context.Context, c *Client, channelID string) (CompactChannel, map[string]any, error) {
	if raw, ok := c.channelInfoCache().get(channelID); ok {
		return ToCompactChannel(raw), raw, nil
	}
	resp, err := c.API(ctx, "conversations.info", map[string]any{"channel": channelID, "include_num_members": true})
	if err != nil {
		return CompactChannel{}, nil, err
	}
	raw := getRec(resp, "channel")
	if getStr(raw, "id") == "" {
		return CompactChannel{}, nil, agenterrors.New("conversations.info returned no channel", agenterrors.FixableByAgent).
			WithHint("check the channel id/name; 'agent-slack channel list' shows conversations")
	}
	compact := ToCompactChannel(raw)
	c.cacheChannelInfo(channelID, raw)            // serve future get / --full from cache
	c.warmChannelCache([]CompactChannel{compact}) // and feed completions/resolution
	return compact, raw, nil
}

// ListChannelMembers returns one page of a channel's member user IDs.
func ListChannelMembers(ctx context.Context, c *Client, channelID string, limit int, cursor string) ([]string, string, error) {
	params := map[string]any{"channel": channelID, "limit": clampInt(limit, 1, 1000)}
	if cursor != "" {
		params["cursor"] = cursor
	}
	resp, err := c.API(ctx, "conversations.members", params)
	if err != nil {
		return nil, "", err
	}
	var ids []string
	for _, m := range getArr(resp, "members") {
		if s, ok := m.(string); ok && s != "" {
			ids = append(ids, s)
		}
	}
	return ids, NextCursor(resp), nil
}

// ResolveChannelName resolves a conversation ID to a readable name — the
// channel name, or the counterpart's display name for DMs. Best effort: any
// failure returns the raw ID rather than an error, because callers only use
// this to decorate output.
func ResolveChannelName(ctx context.Context, c *Client, channelID string) string {
	ch, ok := c.cachedChannel(channelID)
	if !ok {
		resp, err := c.API(ctx, "conversations.info", map[string]any{"channel": channelID})
		if err != nil {
			return channelID
		}
		channel := getRec(resp, "channel")
		if channel == nil {
			return channelID
		}
		ch = ToCompactChannel(channel)
		c.cacheChannel(ch)
	}

	if ch.IsIM {
		if ch.User == "" {
			return channelID
		}
		// The counterpart's display name routes through the (cached) user store.
		users := ResolveUsersByID(ctx, c, []string{ch.User}, false)
		if u, found := users[ch.User]; found {
			if u.DisplayName != "" {
				return u.DisplayName
			}
			if u.RealName != "" {
				return u.RealName
			}
		}
		return channelID
	}

	if ch.Name != "" {
		return ch.Name
	}
	return channelID
}

// MarkConversation marks a channel read up to ts.
func MarkConversation(ctx context.Context, c *Client, channelID, ts string) error {
	_, err := c.API(ctx, "conversations.mark", map[string]any{"channel": channelID, "ts": ts})
	return err
}
