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
	// SlackMarkdown selects the dialect when RawText is converted to draft
	// rich_text blocks: Slack mrkdwn when true, standard Markdown otherwise.
	SlackMarkdown bool
	// UnfurlLinks forces link/media unfurling on — set when forwarding, so the
	// embedded permalink expands into a shared-message card regardless of token
	// type (bot tokens default unfurl_links off).
	UnfurlLinks bool
	// FileIDs attaches already-uploaded files to a draft (drafts.create/update
	// reference ids directly). Unused by chat.postMessage, which uploads +
	// completes its own files.
	FileIDs []string
	// DraftID, when set, tells chat.postMessage which draft this send fulfills:
	// Slack removes that draft as part of the post (the native send-a-draft
	// path), so there is no separate delete to race or leave stale.
	DraftID string
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
	if m.UnfurlLinks {
		params["unfurl_links"] = true
		params["unfurl_media"] = true
	}
	if m.DraftID != "" {
		params["draft_id"] = m.DraftID
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
