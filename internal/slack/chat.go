package slack

import "context"

// OutgoingMessage is one chat.postMessage / chat.scheduleMessage payload.
type OutgoingMessage struct {
	ChannelID      string
	Text           string // escaped for the mrkdwn `text` field
	RawText        string // original text, for building draft rich_text blocks
	ThreadTS       string
	ReplyBroadcast bool
	Blocks         []any
}

func (m OutgoingMessage) params() map[string]any {
	params := map[string]any{"channel": m.ChannelID, "text": m.Text}
	if m.ThreadTS != "" {
		params["thread_ts"] = m.ThreadTS
		if m.ReplyBroadcast {
			params["reply_broadcast"] = true
		}
	}
	if len(m.Blocks) > 0 {
		params["blocks"] = m.Blocks
	}
	return params
}

// PostResult is the decoded chat.postMessage outcome. ChannelID prefers what
// Slack echoes back (DM sends come back as the concrete D… id).
type PostResult struct {
	ChannelID string
	TS        string
}

func PostMessage(ctx context.Context, c *Client, m OutgoingMessage) (PostResult, error) {
	resp, err := c.API(ctx, "chat.postMessage", m.params())
	if err != nil {
		return PostResult{}, err
	}
	return PostResult{
		ChannelID: firstNonEmpty(getStr(resp, "channel"), m.ChannelID),
		TS:        getStr(resp, "ts"),
	}, nil
}

// ScheduleResult is the decoded chat.scheduleMessage outcome. PostAt prefers
// the time Slack echoes (it may round) over what was requested.
type ScheduleResult struct {
	ChannelID          string
	ScheduledMessageID string
	PostAt             int64
}

func ScheduleMessage(ctx context.Context, c *Client, m OutgoingMessage, postAt int64) (ScheduleResult, error) {
	// Browser (xoxc) tokens can't call chat.scheduleMessage; the desktop client
	// schedules by creating a scheduled draft. Drafts require rich_text blocks.
	if c.currentAuth().Type == AuthBrowser {
		d, err := SaveDraft(ctx, c, m, postAt)
		if err != nil {
			return ScheduleResult{}, err
		}
		return ScheduleResult{ChannelID: d.ChannelID, ScheduledMessageID: d.ID, PostAt: d.PostAt}, nil
	}

	params := m.params()
	params["post_at"] = postAt
	resp, err := c.API(ctx, "chat.scheduleMessage", params)
	if err != nil {
		return ScheduleResult{}, err
	}
	out := ScheduleResult{
		ChannelID:          firstNonEmpty(getStr(resp, "channel"), m.ChannelID),
		ScheduledMessageID: getStr(resp, "scheduled_message_id"),
		PostAt:             postAt,
	}
	if at, ok := resp["post_at"].(float64); ok {
		out.PostAt = int64(at)
	}
	return out, nil
}
