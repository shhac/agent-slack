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

	if id := channelIDViaSearch(ctx, c, name); id != "" {
		return id, nil
	}

	found := ""
	err := EachPage(ctx, c, "conversations.list", map[string]any{
		"exclude_archived": true,
		"limit":            200,
		"types":            "public_channel,private_channel",
	}, func(resp map[string]any) (bool, error) {
		for _, ch := range recItems(getArr(resp, "channels")) {
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

// ResolveChannelName resolves a conversation ID to a readable name — the
// channel name, or the counterpart's display name for DMs. Best effort: any
// failure returns the raw ID rather than an error, because callers only use
// this to decorate output.
func ResolveChannelName(ctx context.Context, c *Client, channelID string) string {
	resp, err := c.API(ctx, "conversations.info", map[string]any{"channel": channelID})
	if err != nil {
		return channelID
	}
	channel := getRec(resp, "channel")
	if channel == nil {
		return channelID
	}

	if getBool(channel, "is_im") {
		userID := getStr(channel, "user")
		if userID == "" {
			return channelID
		}
		userResp, err := c.API(ctx, "users.info", map[string]any{"user": userID})
		if err != nil {
			return channelID
		}
		profile := getRec(getRec(userResp, "user"), "profile")
		if displayName := getStr(profile, "display_name"); displayName != "" {
			return displayName
		}
		if realName := getStr(profile, "real_name"); realName != "" {
			return realName
		}
		return channelID
	}

	if name := getStr(channel, "name"); name != "" {
		return name
	}
	return channelID
}
