package slack

import "context"

// ScheduledPage is one page of raw scheduled-message objects.
type ScheduledPage struct {
	ScheduledMessages []map[string]any
	NextCursor        string
}

// ScheduledListOptions controls ListScheduledMessages.
type ScheduledListOptions struct {
	ChannelID      string
	Cursor         string
	Oldest, Latest string
	Limit          int
}

func ListScheduledMessages(ctx context.Context, c *Client, opts ScheduledListOptions) (ScheduledPage, error) {
	page, err := listScheduledPage(ctx, c, opts)
	if err != nil {
		return ScheduledPage{}, err
	}
	// Write-only warm: this list always came fresh from the API; the cache exists
	// solely so shell completion can later suggest these ids (never read here).
	c.warmScheduledCache(page.ScheduledMessages)
	return page, nil
}

func listScheduledPage(ctx context.Context, c *Client, opts ScheduledListOptions) (ScheduledPage, error) {
	// Browser (xoxc) tokens can't call chat.scheduledMessages.list; scheduled
	// messages live as scheduled drafts.
	if c.currentAuth().Type == AuthBrowser {
		drafts, err := listDrafts(ctx, c, true, opts.ChannelID)
		if err != nil {
			return ScheduledPage{}, err
		}
		items := make([]map[string]any, len(drafts))
		for i, d := range drafts {
			items[i] = map[string]any{"id": d.ID, "channel_id": d.ChannelID, "post_at": d.PostAt, "text": d.Text}
		}
		return ScheduledPage{ScheduledMessages: items}, nil
	}

	params := map[string]any{}
	setStr(params, "channel", opts.ChannelID)
	setStr(params, "cursor", opts.Cursor)
	setStr(params, "oldest", opts.Oldest)
	setStr(params, "latest", opts.Latest)
	setPositive(params, "limit", opts.Limit)
	resp, err := c.API(ctx, "chat.scheduledMessages.list", params)
	if err != nil {
		return ScheduledPage{}, err
	}
	return ScheduledPage{
		ScheduledMessages: recItems(getArr(resp, "scheduled_messages")),
		NextCursor:        NextCursor(resp),
	}, nil
}

func CancelScheduledMessage(ctx context.Context, c *Client, channelID, scheduledMessageID string) error {
	// Browser auth: the scheduled message is a draft, cancelled by deleting it
	// (the draft id is globally unique, so channelID is unused here).
	if c.currentAuth().Type == AuthBrowser {
		return DeleteDraft(ctx, c, scheduledMessageID)
	}
	_, err := c.API(ctx, "chat.deleteScheduledMessage", map[string]any{
		"channel":              channelID,
		"scheduled_message_id": scheduledMessageID,
	})
	return err
}
