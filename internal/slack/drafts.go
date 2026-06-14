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
// "scheduled message" as a draft with date_scheduled set, and a plain draft is
// the LLM→human hand-off. chat.scheduleMessage / chat.scheduledMessages.list
// reject client tokens (not_allowed_token_type), so the drafts.* methods back
// scheduling and the `message draft` group on browser auth. Plain drafts are
// one-per-target (a second create returns attached_draft_exists); scheduled
// drafts are many-per-target.

// Draft is the compact projection of one Slack draft.
type Draft struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	PostAt    int64  `json:"post_at,omitempty"` // 0 = a plain (unscheduled) draft
	Text      string `json:"text,omitempty"`
	Blocks    []any  `json:"-"` // raw rich_text, kept for edit/send
}

func toDraft(d map[string]any) Draft {
	return Draft{
		ID:        getStr(d, "id"),
		ChannelID: draftChannelID(d),
		PostAt:    int64(getNum(d, "date_scheduled")),
		Text:      render.RenderMessageContent(d),
		Blocks:    getArr(d, "blocks"),
	}
}

// listDrafts returns active (not deleted/sent) drafts. scheduled selects
// scheduled (date_scheduled>0) vs plain drafts; channelID "" matches all.
func listDrafts(ctx context.Context, c *Client, scheduled bool, channelID string) ([]Draft, error) {
	resp, err := c.API(ctx, "drafts.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out []Draft
	for _, d := range recItems(getArr(resp, "drafts")) {
		if getBool(d, "is_deleted") || getBool(d, "is_sent") {
			continue
		}
		if (getNum(d, "date_scheduled") > 0) != scheduled {
			continue
		}
		if channelID != "" && draftChannelID(d) != channelID {
			continue
		}
		out = append(out, toDraft(d))
	}
	return out, nil
}

// ListDrafts returns the plain (unscheduled) drafts — the `message draft list`
// hand-offs. Scheduled messages are listed by ListScheduledMessages.
func ListDrafts(ctx context.Context, c *Client) ([]Draft, error) {
	return listDrafts(ctx, c, false, "")
}

// PlainDraftForChannel returns the single plain draft for a channel (plain
// drafts are one-per-target), or ok=false when there is none.
func PlainDraftForChannel(ctx context.Context, c *Client, channelID string) (Draft, bool, error) {
	drafts, err := listDrafts(ctx, c, false, channelID)
	if err != nil || len(drafts) == 0 {
		return Draft{}, false, err
	}
	return drafts[0], true, nil
}

// SaveDraft creates a draft from an outgoing message: PostAt 0 is a plain draft,
// PostAt > 0 is a scheduled message. Browser auth only.
func SaveDraft(ctx context.Context, c *Client, m OutgoingMessage, postAt int64) (Draft, error) {
	params := draftContent(m, postAt)
	params["client_msg_id"] = newClientMsgID()
	return createDraft(ctx, c, "drafts.create", params)
}

// UpdateDraft replaces a draft's content; postAt 0 keeps it a plain draft,
// postAt > 0 promotes it to a scheduled message in place. Browser auth only.
func UpdateDraft(ctx context.Context, c *Client, draftID string, m OutgoingMessage, postAt int64) (Draft, error) {
	params := draftContent(m, postAt)
	params["draft_id"] = draftID
	params["client_last_updated_ts"] = draftClientTS()
	return createDraft(ctx, c, "drafts.update", params)
}

// DeleteDraft soft-deletes a draft by id. client_last_updated_ts is the client's
// current wall-clock — a fresh value always wins the last-writer-wins check.
func DeleteDraft(ctx context.Context, c *Client, draftID string) error {
	_, err := c.API(ctx, "drafts.delete", map[string]any{
		"draft_id":               draftID,
		"client_last_updated_ts": draftClientTS(),
	})
	return err
}

// draftContent is the shared create/update body. postAt > 0 makes a scheduled
// (composer) draft; a plain draft has no schedule and is not composer-attached.
func draftContent(m OutgoingMessage, postAt int64) map[string]any {
	params := map[string]any{
		"blocks":           draftBlocks(m),
		"destinations":     []any{map[string]any{"channel_id": m.ChannelID}},
		"file_ids":         []any{},
		"is_from_composer": postAt > 0,
	}
	if postAt > 0 {
		params["date_scheduled"] = postAt
	}
	return params
}

func createDraft(ctx context.Context, c *Client, method string, params map[string]any) (Draft, error) {
	resp, err := c.API(ctx, method, params)
	if err != nil {
		return Draft{}, err
	}
	return toDraft(getRec(resp, "draft")), nil
}

// draftBlocks returns the rich_text blocks to store: the message's own blocks
// when present (structured text or --blocks), otherwise a rich_text block built
// from the raw text (a draft has no plain-text field).
func draftBlocks(m OutgoingMessage) []any {
	if len(m.Blocks) > 0 {
		return m.Blocks
	}
	var out []any
	for _, b := range render.RichTextBlocksForText(m.RawText, render.RichTextOptions{SlackMarkdown: m.SlackMarkdown}) {
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
