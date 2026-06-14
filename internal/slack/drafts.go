package slack

import (
	"context"
	"crypto/rand"
	"fmt"
	"strconv"
	"time"

	"github.com/shhac/agent-slack/internal/render"
)

// Drafts are a client-only (xoxc) concept. The browser/desktop client stores a
// "scheduled message" as a draft with date_scheduled set, listed/created/
// deleted via the drafts.* methods — chat.scheduleMessage and
// chat.scheduledMessages.list reject client tokens (not_allowed_token_type).
// These functions back the scheduled-message commands on browser auth and the
// `message draft` command.

// DraftInput describes a draft to create.
type DraftInput struct {
	ChannelID string
	Blocks    []any // rich_text blocks (required — drafts have no text field)
	PostAt    int64 // 0 = a plain draft; > 0 = scheduled for that unix time
}

// DraftResult is the created draft.
type DraftResult struct {
	ID        string
	ChannelID string
	PostAt    int64
}

// SaveDraft creates a draft from an outgoing message: PostAt 0 is a plain draft
// (the `message draft` hand-off), PostAt > 0 is a scheduled message. Browser
// (xoxc) auth only — drafts are a client feature.
func SaveDraft(ctx context.Context, c *Client, m OutgoingMessage, postAt int64) (DraftResult, error) {
	return createDraft(ctx, c, DraftInput{ChannelID: m.ChannelID, Blocks: draftBlocks(m), PostAt: postAt})
}

// createDraft creates a draft. A scheduled draft (PostAt > 0) must be a
// composer draft; a plain draft is the `message draft` hand-off.
func createDraft(ctx context.Context, c *Client, in DraftInput) (DraftResult, error) {
	params := map[string]any{
		"client_msg_id":    newClientMsgID(),
		"blocks":           in.Blocks,
		"destinations":     []any{map[string]any{"channel_id": in.ChannelID}},
		"file_ids":         []any{},
		"is_from_composer": in.PostAt > 0,
	}
	if in.PostAt > 0 {
		params["date_scheduled"] = in.PostAt
	}
	resp, err := c.API(ctx, "drafts.create", params)
	if err != nil {
		return DraftResult{}, err
	}
	draft := getRec(resp, "draft")
	return DraftResult{
		ID:        getStr(draft, "id"),
		ChannelID: draftChannelID(draft),
		PostAt:    int64(getNum(draft, "date_scheduled")),
	}, nil
}

// listScheduledDrafts returns the scheduled (and not deleted/sent) drafts,
// shaped like the chat.scheduledMessages.list items the standard path emits:
// id, channel_id, post_at, text. channelID "" means all channels.
func listScheduledDrafts(ctx context.Context, c *Client, channelID string) ([]map[string]any, error) {
	resp, err := c.API(ctx, "drafts.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, d := range recItems(getArr(resp, "drafts")) {
		if getNum(d, "date_scheduled") <= 0 || getBool(d, "is_deleted") || getBool(d, "is_sent") {
			continue
		}
		ch := draftChannelID(d)
		if channelID != "" && ch != channelID {
			continue
		}
		out = append(out, map[string]any{
			"id":         getStr(d, "id"),
			"channel_id": ch,
			"post_at":    int64(getNum(d, "date_scheduled")),
			"text":       render.RenderMessageContent(d),
		})
	}
	return out, nil
}

// deleteDraft soft-deletes a draft. client_last_updated_ts is the client's
// current wall-clock — a fresh value always wins the last-writer-wins check
// (the stored last_updated_ts is not what the server compares against).
func deleteDraft(ctx context.Context, c *Client, draftID string) error {
	_, err := c.API(ctx, "drafts.delete", map[string]any{
		"draft_id":               draftID,
		"client_last_updated_ts": draftClientTS(),
	})
	return err
}

// draftBlocks returns the rich_text blocks to store for a draft: the message's
// own blocks when present (structured text or --blocks), otherwise a rich_text
// block built from the raw text (a draft has no plain-text field, so plain
// text must still become blocks).
func draftBlocks(m OutgoingMessage) []any {
	if len(m.Blocks) > 0 {
		return m.Blocks
	}
	var out []any
	for _, b := range render.RichTextBlocksForText(m.RawText) {
		out = append(out, b)
	}
	return out
}

func draftChannelID(draft map[string]any) string {
	dests := recItems(getArr(draft, "destinations"))
	if len(dests) == 0 {
		return ""
	}
	return getStr(dests[0], "channel_id")
}

func draftClientTS() string {
	return strconv.FormatFloat(float64(time.Now().UnixMicro())/1e6, 'f', 6, 64)
}

// newClientMsgID is the per-draft client message id (a UUIDv4 like the web
// client sends).
func newClientMsgID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
