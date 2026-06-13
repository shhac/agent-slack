package slack

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"github.com/shhac/agent-slack/internal/render"
)

// UnreadChannel summarizes one conversation with unread activity.
type UnreadChannel struct {
	ChannelID    string          `json:"channel_id"`
	ChannelName  string          `json:"channel_name,omitempty"`
	ChannelType  string          `json:"channel_type"` // channel | dm | mpim
	UnreadCount  int             `json:"unread_count"`
	MentionCount int             `json:"mention_count"`
	Messages     []UnreadMessage `json:"messages,omitempty"`
}

type UnreadMessage struct {
	TS         string                `json:"ts"`
	Author     *render.CompactAuthor `json:"author,omitempty"`
	Content    string                `json:"content,omitempty"`
	ThreadTS   string                `json:"thread_ts,omitempty"`
	ReplyCount int                   `json:"reply_count,omitempty"`
}

type UnreadThreads struct {
	HasUnreads   bool `json:"has_unreads"`
	MentionCount int  `json:"mention_count"`
}

type UnreadsResult struct {
	Channels []UnreadChannel `json:"channels"`
	Threads  *UnreadThreads  `json:"threads,omitempty"`
}

// UnreadsOptions controls FetchUnreads.
type UnreadsOptions struct {
	IncludeMessages       bool
	MaxMessagesPerChannel int // default 10
	MaxBodyChars          int // 0 → 4000, negative → unlimited
	SkipSystemMessages    bool
}

var systemSubtypes = map[string]bool{
	"channel_join": true, "channel_leave": true, "channel_topic": true,
	"channel_purpose": true, "channel_name": true, "channel_archive": true,
	"channel_unarchive": true, "group_join": true, "group_leave": true,
	"group_topic": true, "group_purpose": true, "group_name": true,
	"group_archive": true, "group_unarchive": true,
}

// FetchUnreads reads client.counts (internal API: browser auth) and hydrates
// each unread conversation with its name and the messages since last_read.
func FetchUnreads(ctx context.Context, c *Client, opts UnreadsOptions) (UnreadsResult, error) {
	maxMessages := opts.MaxMessagesPerChannel
	if maxMessages == 0 {
		maxMessages = 10
	}
	maxBodyChars := opts.MaxBodyChars
	if maxBodyChars == 0 {
		maxBodyChars = 4000
	}

	resp, err := c.API(ctx, "client.counts", map[string]any{"thread_count_by_channel": true})
	if err != nil {
		return UnreadsResult{}, err
	}

	type entry struct {
		raw         map[string]any
		channelType string
	}
	var entries []entry
	for _, ch := range recItems(getArr(resp, "channels")) {
		entries = append(entries, entry{ch, "channel"})
	}
	for _, ch := range recItems(getArr(resp, "mpims")) {
		entries = append(entries, entry{ch, "mpim"})
	}
	for _, ch := range recItems(getArr(resp, "ims")) {
		entries = append(entries, entry{ch, "dm"})
	}

	var withUnreads []entry
	for _, e := range entries {
		if getBool(e.raw, "has_unreads") {
			withUnreads = append(withUnreads, e)
		}
	}

	// Each channel needs 1-2 extra calls; bound the fan-out.
	channels := make([]UnreadChannel, len(withUnreads))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, e := range withUnreads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			channels[i] = hydrateUnreadChannel(ctx, c, e.raw, e.channelType, hydrateOptions{
				includeMessages: opts.IncludeMessages,
				maxMessages:     maxMessages,
				maxBodyChars:    maxBodyChars,
				skipSystem:      opts.SkipSystemMessages,
			})
		}()
	}
	wg.Wait()

	// Mentions first, then by unread volume.
	sort.SliceStable(channels, func(i, j int) bool {
		if channels[i].MentionCount != channels[j].MentionCount {
			return channels[i].MentionCount > channels[j].MentionCount
		}
		return channels[i].UnreadCount > channels[j].UnreadCount
	})

	result := UnreadsResult{Channels: channels}
	if threads := getRec(resp, "threads"); getBool(threads, "has_unreads") {
		result.Threads = &UnreadThreads{HasUnreads: true, MentionCount: int(getNum(threads, "mention_count"))}
	}
	return result, nil
}

type hydrateOptions struct {
	includeMessages bool
	maxMessages     int
	maxBodyChars    int
	skipSystem      bool
}

func hydrateUnreadChannel(ctx context.Context, c *Client, raw map[string]any, channelType string, opts hydrateOptions) UnreadChannel {
	channelID := getStr(raw, "id")
	out := UnreadChannel{
		ChannelID:    channelID,
		ChannelType:  channelType,
		MentionCount: int(getNum(raw, "mention_count")),
	}

	// Name + corrected type from conversations.info (DMs resolve to the
	// counterpart's display name). Best effort.
	if info, err := c.API(ctx, "conversations.info", map[string]any{"channel": channelID}); err == nil {
		if ch := getRec(info, "channel"); ch != nil {
			out.ChannelName, out.ChannelType = channelIdentity(ch)
			if out.ChannelType == "dm" && out.ChannelName == "" {
				if userID := getStr(ch, "user"); userID != "" {
					out.ChannelName = dmCounterpartName(ctx, c, userID)
				}
			}
		}
	}

	count, hasCount := rawUnreadCount(raw)
	out.UnreadCount = count

	lastRead := getStr(raw, "last_read")
	if !opts.includeMessages || lastRead == "" {
		return out
	}

	msgs, inferred := fetchUnreadMessages(ctx, c, channelID, lastRead, hasCount, opts)
	out.Messages = msgs
	if inferred >= 0 {
		out.UnreadCount = inferred
	}
	return out
}

// fetchUnreadMessages pulls conversations.history since lastRead, drops system
// messages when asked, and shapes the rest into UnreadMessages (oldest first).
// When the counts API gave no number (hasCount is false) it also infers an
// unread count from what it fetched, returned as inferredCount; otherwise
// inferredCount is -1. Best effort: a history error yields no messages.
func fetchUnreadMessages(ctx context.Context, c *Client, channelID, lastRead string, hasCount bool, opts hydrateOptions) (msgs []UnreadMessage, inferredCount int) {
	inferredCount = -1
	history, err := c.API(ctx, "conversations.history", map[string]any{
		"channel":   channelID,
		"oldest":    lastRead,
		"limit":     opts.maxMessages,
		"inclusive": false,
	})
	if err != nil {
		return nil, -1
	}

	var kept []map[string]any
	for _, m := range recItems(getArr(history, "messages")) {
		if opts.skipSystem {
			if subtype := getStr(m, "subtype"); subtype != "" && systemSubtypes[subtype] {
				continue
			}
		}
		kept = append(kept, m)
	}

	if !hasCount {
		inferredCount = len(kept)
		if getBool(history, "has_more") && inferredCount < 2 {
			inferredCount = 2
		}
	}

	for _, m := range kept {
		inline := toInlineMessage(m, opts.maxBodyChars)
		msgs = append(msgs, UnreadMessage{
			TS:         getStr(m, "ts"),
			Author:     inline.Author,
			Content:    inline.Content,
			ThreadTS:   inline.ThreadTS,
			ReplyCount: inline.ReplyCount,
		})
	}
	sort.SliceStable(msgs, func(i, j int) bool {
		a, _ := strconv.ParseFloat(msgs[i].TS, 64)
		b, _ := strconv.ParseFloat(msgs[j].TS, 64)
		return a < b
	})
	return msgs, inferredCount
}

// channelIdentity derives a conversation's display name and corrected type
// from a raw conversations.info channel record. Pure: the DM-counterpart
// lookup (which needs another API call) stays with the caller.
func channelIdentity(ch map[string]any) (name, chType string) {
	name = getStr(ch, "name")
	if name == "" {
		name = getStr(ch, "name_normalized")
	}
	switch {
	case getBool(ch, "is_im"):
		return name, "dm"
	case getBool(ch, "is_mpim"):
		return name, "mpim"
	default:
		return name, "channel"
	}
}

// rawUnreadCount reads the unread count from a client.counts entry: Slack
// sends unread_count_display, or unread_count, or neither (hasCount=false →
// the caller infers from fetched messages, defaulting to 1).
func rawUnreadCount(raw map[string]any) (count int, hasCount bool) {
	if n, ok := raw["unread_count_display"].(float64); ok {
		return int(n), true
	}
	if n, ok := raw["unread_count"].(float64); ok {
		return int(n), true
	}
	return 1, false
}

func dmCounterpartName(ctx context.Context, c *Client, userID string) string {
	resp, err := c.API(ctx, "users.info", map[string]any{"user": userID})
	if err != nil {
		return ""
	}
	user := getRec(resp, "user")
	profile := getRec(user, "profile")
	if name := getStr(profile, "display_name"); name != "" {
		return name
	}
	if name := getStr(user, "real_name"); name != "" {
		return name
	}
	return getStr(user, "name")
}
