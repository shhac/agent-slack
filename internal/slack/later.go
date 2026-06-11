package slack

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// LaterItem is one saved-for-later message.
type LaterItem struct {
	ChannelID     string        `json:"channel_id"`
	ChannelName   string        `json:"channel_name,omitempty"`
	TS            string        `json:"ts"`
	State         string        `json:"state"`
	DateSaved     int64         `json:"date_saved"`
	DateCompleted int64         `json:"date_completed,omitempty"`
	Message       *LaterMessage `json:"message,omitempty"`
}

type LaterMessage struct {
	Author     *render.CompactAuthor `json:"author,omitempty"`
	Content    string                `json:"content,omitempty"`
	ThreadTS   string                `json:"thread_ts,omitempty"`
	ReplyCount int                   `json:"reply_count,omitempty"`
}

type LaterCounts struct {
	InProgress int `json:"in_progress"`
	Archived   int `json:"archived"`
	Completed  int `json:"completed"`
	Total      int `json:"total"`
}

type LaterResult struct {
	Counts     LaterCounts `json:"counts"`
	Items      []LaterItem `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// LaterOptions controls FetchLaterItems.
type LaterOptions struct {
	State        string // in_progress (default) | archived | completed | all
	Limit        int    // default 20
	MaxBodyChars int    // 0 → 4000, negative → unlimited
	CountsOnly   bool
	Cursor       string
}

// FetchLaterItems lists the Later tab: pages saved.list until enough
// message-type items in the requested state accumulate, then hydrates each
// with its channel name and rendered message content.
func FetchLaterItems(ctx context.Context, c *Client, opts LaterOptions) (LaterResult, error) {
	state := opts.State
	if state == "" {
		state = "in_progress"
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 20
	}
	maxBodyChars := opts.MaxBodyChars
	if maxBodyChars == 0 {
		maxBodyChars = 4000
	}

	var allRaw []map[string]any
	var counts map[string]any
	nextCursor := ""
	params := map[string]any{"limit": 50}
	if opts.Cursor != "" {
		params["cursor"] = opts.Cursor
	}
	firstPage := true
	err := EachPage(ctx, c, "saved.list", params, func(resp map[string]any) (bool, error) {
		if firstPage {
			counts = getRec(resp, "counts")
			firstPage = false
		}
		allRaw = append(allRaw, recItems(getArr(resp, "saved_items"))...)
		nextCursor = NextCursor(resp)
		if opts.CountsOnly {
			return false, nil
		}
		return len(filterLaterItems(allRaw, state)) < limit, nil
	})
	if err != nil {
		return LaterResult{}, err
	}

	result := LaterResult{
		Counts: LaterCounts{
			InProgress: int(getNum(counts, "uncompleted_count")),
			Archived:   int(getNum(counts, "archived_count")),
			Completed:  int(getNum(counts, "completed_count")),
			Total:      int(getNum(counts, "total_count")),
		},
		NextCursor: nextCursor,
	}
	if opts.CountsOnly {
		return result, nil
	}

	filtered := filterLaterItems(allRaw, state)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	for _, item := range filtered {
		channelID := getStr(item, "item_id")
		ts := getStr(item, "ts")
		out := LaterItem{
			ChannelID: channelID,
			TS:        ts,
			State:     getStr(item, "state"),
			DateSaved: int64(getNum(item, "date_created")),
		}
		if out.State == "" {
			out.State = "in_progress"
		}
		if completed := int64(getNum(item, "date_completed")); completed > 0 {
			out.DateCompleted = completed
		}
		out.ChannelName = ResolveChannelName(ctx, c, channelID)
		if out.ChannelName == channelID {
			out.ChannelName = ""
		}
		if ts != "" {
			out.Message = fetchLaterMessage(ctx, c, channelID, ts, maxBodyChars)
		}
		result.Items = append(result.Items, out)
	}
	return result, nil
}

func filterLaterItems(items []map[string]any, state string) []map[string]any {
	var out []map[string]any
	for _, item := range items {
		if getStr(item, "item_type") != "message" {
			continue
		}
		if state != "all" && getStr(item, "state") != state {
			continue
		}
		out = append(out, item)
	}
	return out
}

// fetchLaterMessage hydrates the saved message body; best effort — the
// message may have been deleted.
func fetchLaterMessage(ctx context.Context, c *Client, channelID, ts string, maxBodyChars int) *LaterMessage {
	history, err := c.API(ctx, "conversations.history", map[string]any{
		"channel":   channelID,
		"latest":    ts,
		"inclusive": true,
		"limit":     1,
	})
	if err != nil {
		return nil
	}
	msg := findByTS(getArr(history, "messages"), ts)
	if msg == nil {
		return nil
	}
	content := render.TruncateBody(render.RenderMessageContent(msg), maxBodyChars)
	out := &LaterMessage{
		Content:    content,
		ThreadTS:   getStr(msg, "thread_ts"),
		ReplyCount: int(getNum(msg, "reply_count")),
	}
	if user, bot := getStr(msg, "user"), getStr(msg, "bot_id"); user != "" || bot != "" {
		out.Author = &render.CompactAuthor{UserID: user, BotID: bot}
	}
	return out
}

// ParseLaterState normalizes the --state flag's aliases.
func ParseLaterState(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "in_progress", "in-progress", "active", "open":
		return "in_progress", nil
	case "archived", "archive":
		return "archived", nil
	case "completed", "complete", "done":
		return "completed", nil
	case "all":
		return "all", nil
	default:
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "invalid --state %q", raw).
			WithHint("use in_progress, archived, completed, or all")
	}
}

// UpdateLaterMark completes/uncompletes/archives/unarchives a saved item.
// saved.update requires multipart encoding — urlencoded params are silently
// ignored.
func UpdateLaterMark(ctx context.Context, c *Client, channelID, ts, mark string) error {
	_, err := c.APIMultipart(ctx, "saved.update", map[string]any{
		"item_id":   channelID,
		"item_type": "message",
		"ts":        ts,
		"mark":      mark,
	})
	return err
}

func SaveLater(ctx context.Context, c *Client, channelID, ts string) error {
	_, err := c.API(ctx, "saved.add", map[string]any{
		"item_id":   channelID,
		"item_type": "message",
		"ts":        ts,
	})
	return err
}

func RemoveLater(ctx context.Context, c *Client, channelID, ts string) error {
	_, err := c.API(ctx, "saved.delete", map[string]any{
		"item_id":   channelID,
		"item_type": "message",
		"ts":        ts,
	})
	return err
}

func SetLaterReminder(ctx context.Context, c *Client, channelID, ts string, dateDue int64) error {
	_, err := c.APIMultipart(ctx, "saved.update", map[string]any{
		"item_id":   channelID,
		"item_type": "message",
		"ts":        ts,
		"date_due":  strconv.FormatInt(dateDue, 10),
	})
	return err
}

// ParseReminderDuration turns "30m", "2d", "tomorrow", "monday", or a unix
// timestamp into an absolute unix time (named days resolve to 9am local).
func ParseReminderDuration(input string, now time.Time) (int64, error) {
	trimmed := strings.ToLower(strings.TrimSpace(input))

	if m := relativeDurationRe.FindStringSubmatch(trimmed); m != nil {
		amount, _ := strconv.ParseFloat(m[1], 64)
		switch m[2][0] {
		case 'm':
			return now.Unix() + int64(amount*60), nil
		case 'h':
			return now.Unix() + int64(amount*3600), nil
		case 'd':
			return now.Unix() + int64(amount*86400), nil
		}
	}

	if trimmed == "tomorrow" {
		return nineAM(now, 1), nil
	}
	dayNames := []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	for i, day := range dayNames {
		if trimmed == day {
			daysUntil := i - int(now.Weekday())
			if daysUntil <= 0 {
				daysUntil += 7
			}
			return nineAM(now, daysUntil), nil
		}
	}

	if n, err := strconv.ParseFloat(trimmed, 64); err == nil && n > 1_000_000_000 {
		return int64(n), nil
	}

	return 0, agenterrors.New(fmt.Sprintf("invalid duration: %q", input), agenterrors.FixableByAgent).
		WithHint("use: 30m, 1h, 3h, 2d, tomorrow, monday, or a unix timestamp")
}

func nineAM(now time.Time, daysFromNow int) int64 {
	d := now.AddDate(0, 0, daysFromNow)
	return time.Date(d.Year(), d.Month(), d.Day(), 9, 0, 0, 0, now.Location()).Unix()
}
