package slack

import (
	"context"
	"time"
)

// WarmOptions configures a cache warm sweep.
type WarmOptions struct {
	// PageDelay pauses between paged API calls to stay under Slack's rate
	// limits (the client also backs off on a 429). Zero disables the pause.
	PageDelay   time.Duration
	IncludeBots bool // include bot users in the users warm
}

// WarmEvent is one progress record emitted as a warm sweep proceeds: a
// running tally per page, then a Done record at each category boundary.
type WarmEvent struct {
	Category string `json:"category"` // users | channels | usergroups
	Count    int    `json:"count"`    // entities warmed so far in this category
	Done     bool   `json:"done,omitempty"`
}

// WarmWorkspace pre-fetches the workspace's list endpoints (users, channels,
// usergroups) and writes them into the per-workspace cache, so later name/handle
// resolution and shell completions are instant and need no network. It
// paginates each endpoint to completion, pausing opts.PageDelay between pages.
// progress, when non-nil, is called after each page and at each category
// boundary — stream it as JSONL so a long sweep shows life.
func WarmWorkspace(ctx context.Context, c *Client, opts WarmOptions, progress func(WarmEvent)) error {
	emit := func(e WarmEvent) {
		if progress != nil {
			progress(e)
		}
	}
	// pace pauses between pages, but only when more pages follow (no trailing
	// sleep after the last page of a category).
	pace := func(resp map[string]any) error {
		if opts.PageDelay <= 0 || NextCursor(resp) == "" {
			return nil
		}
		return c.sleep(ctx, opts.PageDelay)
	}

	if err := warmUsers(ctx, c, opts, emit, pace); err != nil {
		return err
	}
	if err := warmChannels(ctx, c, emit, pace); err != nil {
		return err
	}
	// usergroups.list has no pagination; fetchUsergroups warms both the entity
	// store and the handle index.
	groups, err := fetchUsergroups(ctx, c, true)
	if err != nil {
		return err
	}
	emit(WarmEvent{Category: "usergroups", Count: len(groups), Done: true})
	return nil
}

func warmUsers(ctx context.Context, c *Client, opts WarmOptions, emit func(WarmEvent), pace func(map[string]any) error) error {
	var users []CompactUser
	err := EachPage(ctx, c, "users.list", map[string]any{"limit": 200}, func(resp map[string]any) (bool, error) {
		for _, m := range recItems(getArr(resp, "members")) {
			if getStr(m, "id") == "" {
				continue
			}
			if !opts.IncludeBots && getBool(m, "is_bot") {
				continue
			}
			users = append(users, ToCompactUser(m))
		}
		emit(WarmEvent{Category: "users", Count: len(users)})
		return true, pace(resp)
	})
	if err != nil {
		return err
	}
	// Annotate open DMs (best-effort) so warmed profiles carry dm_id, matching
	// `user list`.
	if dmMap, derr := fetchDMMap(ctx, c); derr == nil {
		for i := range users {
			users[i].DMID = dmMap[users[i].ID]
		}
	}
	c.warmUserCache(users)
	emit(WarmEvent{Category: "users", Count: len(users), Done: true})
	return nil
}

func warmChannels(ctx context.Context, c *Client, emit func(WarmEvent), pace func(map[string]any) error) error {
	count := 0
	err := EachPage(ctx, c, "conversations.list", map[string]any{
		"limit":            200,
		"types":            defaultConversationTypes,
		"exclude_archived": true,
	}, func(resp map[string]any) (bool, error) {
		page := recItems(getArr(resp, "channels"))
		warm := make([]CompactChannel, 0, len(page))
		for _, ch := range page {
			warm = append(warm, ToCompactChannel(ch))
		}
		c.warmChannelCache(warm)
		count += len(warm)
		emit(WarmEvent{Category: "channels", Count: count})
		return true, pace(resp)
	})
	if err != nil {
		return err
	}
	emit(WarmEvent{Category: "channels", Count: count, Done: true})
	return nil
}
