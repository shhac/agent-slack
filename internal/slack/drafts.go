package slack

import (
	"context"
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shhac/agent-slack/internal/render"
)

// Drafts are a client-only (xoxc) concept. The browser/desktop client stores a
// "scheduled message" as a draft with date_scheduled set, and a plain draft is
// the LLM→human hand-off. chat.scheduleMessage / chat.scheduledMessages.list
// reject client tokens (not_allowed_token_type), so the drafts.* methods back
// scheduling and the `message draft` group on browser auth.
//
// Slack enforces its one-per-target dedup ONLY on the plain hand-off slot
// (is_from_composer=false); the other kinds are unrestricted, so a target's
// draft kinds are:
//   - the plain hand-off draft — is_from_composer=false, date_scheduled=0 (ONE)
//   - live UI composer drafts  — is_from_composer=true,  date_scheduled=0 (MANY)
//   - scheduled messages       — is_from_composer=true,  date_scheduled>0 (MANY)
// We manage only the first: a second hand-off create returns attached_draft_exists,
// so it is genuinely one-per-target. Composer drafts are the user's in-app compose
// boxes for that channel (there can be several) — we must NOT list, send, or
// delete them, or we'd fire off whatever they happen to be typing.

// Draft is the compact projection of one Slack draft.
type Draft struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channel_id"`
	PostAt    int64    `json:"post_at,omitempty"` // 0 = a plain (unscheduled) draft
	Text      string   `json:"text,omitempty"`
	Blocks    []any    `json:"-"`                  // raw rich_text, kept for edit/send
	FileIDs   []string `json:"file_ids,omitempty"` // already-uploaded attachments, kept for send
}

func toDraft(d map[string]any) Draft {
	return Draft{
		ID:        getStr(d, "id"),
		ChannelID: draftChannelID(d),
		PostAt:    int64(getNum(d, "date_scheduled")),
		Text:      render.RenderMessageContent(d),
		Blocks:    getArr(d, "blocks"),
		FileIDs:   draftFileIDs(d),
	}
}

// draftFileIDs reads a draft's file_ids (Slack stores them as a string array).
func draftFileIDs(d map[string]any) []string {
	raw := getArr(d, "file_ids")
	if len(raw) == 0 {
		return nil
	}
	ids := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			ids = append(ids, s)
		}
	}
	return ids
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
		// A plain hand-off draft is is_from_composer=false; an unscheduled
		// composer draft (the user's live compose box) shares the same target
		// and date_scheduled=0, so exclude it from the plain view.
		if !scheduled && getBool(d, "is_from_composer") {
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

// ShareDraft sends a draft that carries attachments via files.share — the
// native "send message with files" path the web client uses. It posts the
// draft's blocks together with its already-uploaded files and removes the draft
// in one call. chat.postMessage can't re-attach pre-uploaded files, so a draft
// with attachments must go this way (a fileless draft posts normally instead).
// Browser auth only.
func ShareDraft(ctx context.Context, c *Client, d Draft) (PostResult, error) {
	resp, err := c.API(ctx, "files.share", map[string]any{
		"draft_id":      d.ID,
		"files":         strings.Join(d.FileIDs, ","),
		"channel":       d.ChannelID,
		"blocks":        d.Blocks,
		"client_msg_id": newClientMsgID(),
		"broadcast":     false,
	})
	if err != nil {
		return PostResult{}, err
	}
	return PostResult{ChannelID: d.ChannelID, TS: getStr(resp, "file_msg_ts")}, nil
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
	fileIDs := make([]any, len(m.FileIDs))
	for i, id := range m.FileIDs {
		fileIDs[i] = id
	}
	params := map[string]any{
		"blocks":           draftBlocks(m),
		"destinations":     []any{map[string]any{"channel_id": m.ChannelID}},
		"file_ids":         fileIDs,
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
