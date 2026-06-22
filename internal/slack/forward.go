package slack

import (
	"context"
	"strings"
)

// ForwardSource is the message a forward points at: the source conversation and
// message ts, plus its permalink for the unfurl fallback.
type ForwardSource struct {
	ChannelID string
	TS        string
	Permalink string
}

// ForwardMessage forwards src into destChannelID with an optional caption.
//
// Browser (xoxc) auth uses chat.shareMessage — the same internal method Slack's
// client drives for "Forward message" — producing a genuine is_share card: the
// original's content is embedded with a "View conversation" control and no raw
// URL. Other tokens can't call it, so they fall back to posting the permalink
// and letting Slack unfurl it into a shared-message card; that unfurl is
// permission-scoped to the source channel, where a real forward is not.
func ForwardMessage(ctx context.Context, c *Client, destChannelID string, src ForwardSource, caption OutgoingMessage) (PostResult, error) {
	if c.currentAuth().Type == AuthBrowser {
		params := map[string]any{
			"channel":       src.ChannelID, // the message being shared lives here
			"timestamp":     src.TS,
			"share_channel": destChannelID, // ...and is shared into here
			"text":          caption.Text,
		}
		if len(caption.Blocks) > 0 {
			params["blocks"] = caption.Blocks
		}
		resp, err := c.API(ctx, "chat.shareMessage", params)
		if err != nil {
			return PostResult{}, err
		}
		return PostResult{
			ChannelID: FirstNonEmpty(getStr(resp, "channel"), destChannelID),
			TS:        getStr(resp, "ts"),
		}, nil
	}

	// Fallback: embed the permalink so Slack unfurls it. unfurl_links is forced
	// on because bot tokens default it off.
	text := caption.Text
	if strings.TrimSpace(text) == "" {
		text = src.Permalink
	} else {
		text += "\n\n" + src.Permalink
	}
	return PostMessage(ctx, c, OutgoingMessage{ChannelID: destChannelID, Text: text, UnfurlLinks: true})
}
