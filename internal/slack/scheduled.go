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
	params := map[string]any{}
	if opts.ChannelID != "" {
		params["channel"] = opts.ChannelID
	}
	if opts.Cursor != "" {
		params["cursor"] = opts.Cursor
	}
	if opts.Oldest != "" {
		params["oldest"] = opts.Oldest
	}
	if opts.Latest != "" {
		params["latest"] = opts.Latest
	}
	if opts.Limit > 0 {
		params["limit"] = opts.Limit
	}
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
	_, err := c.API(ctx, "chat.deleteScheduledMessage", map[string]any{
		"channel":              channelID,
		"scheduled_message_id": scheduledMessageID,
	})
	return err
}
