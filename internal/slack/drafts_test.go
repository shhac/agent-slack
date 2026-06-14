package slack

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func browserClient(t *testing.T, server *mockslack.Server) *Client {
	t.Helper()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return New(Auth{Type: AuthBrowser, XOXC: "xoxc-test", XOXD: "d", WorkspaceURL: ts.URL})
}

func richTextBlock(text string) map[string]any {
	return map[string]any{"type": "rich_text", "elements": []any{
		map[string]any{"type": "rich_text_section", "elements": []any{
			map[string]any{"type": "text", "text": text}}}}}
}

func TestSaveDraftScheduled(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0AAA1", "date_scheduled": float64(123456),
		"destinations": []any{map[string]any{"channel_id": "C1"}}}})
	c := browserClient(t, server)

	res, err := SaveDraft(context.Background(), c, OutgoingMessage{ChannelID: "C1", RawText: "hello"}, 123456)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "Dr0AAA1" || res.PostAt != 123456 || res.ChannelID != "C1" {
		t.Errorf("result = %+v", res)
	}
	call := server.CallsFor("drafts.create")[0]
	if call.Params.Get("is_from_composer") != "true" {
		t.Errorf("scheduled draft must be is_from_composer=true: %v", call.Params)
	}
	if call.Params.Get("date_scheduled") != "123456" {
		t.Errorf("date_scheduled = %q", call.Params.Get("date_scheduled"))
	}
	blocks := call.Params.Get("blocks")
	if !strings.Contains(blocks, "rich_text") || !strings.Contains(blocks, "hello") {
		// Drafts have no plain-text field, so the raw text must survive into the
		// rich_text blocks — a mangling/escaping bug here corrupts the message.
		t.Errorf("draft blocks must carry the raw text as rich_text: %s", blocks)
	}
	if !call.Params.Has("file_ids") {
		t.Error("drafts.create requires file_ids")
	}
}

func TestSaveDraftPlain(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0PLN1", "destinations": []any{map[string]any{"channel_id": "C1"}}}})
	c := browserClient(t, server)

	if _, err := SaveDraft(context.Background(), c, OutgoingMessage{ChannelID: "C1", RawText: "hi"}, 0); err != nil {
		t.Fatal(err)
	}
	call := server.CallsFor("drafts.create")[0]
	if call.Params.Get("is_from_composer") != "false" {
		t.Errorf("a plain draft is not a composer draft: %v", call.Params)
	}
	if call.Params.Has("date_scheduled") {
		t.Error("a plain draft must not set date_scheduled")
	}
}

func TestSaveDraftUsesProvidedBlocks(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{"id": "Dr0B"}})
	c := browserClient(t, server)

	// When the message already has blocks (--blocks or structured text), they
	// pass through verbatim rather than being re-derived from raw text.
	m := OutgoingMessage{ChannelID: "C1", RawText: "ignored", Blocks: []any{richTextBlock("kept verbatim")}}
	if _, err := SaveDraft(context.Background(), c, m, 0); err != nil {
		t.Fatal(err)
	}
	blocks := server.CallsFor("drafts.create")[0].Params.Get("blocks")
	if !strings.Contains(blocks, "kept verbatim") || strings.Contains(blocks, "ignored") {
		t.Errorf("provided blocks should pass through unchanged: %s", blocks)
	}
}

func TestUpdateDraft(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.update", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C1"}}}})
	c := browserClient(t, server)

	if _, err := UpdateDraft(context.Background(), c, "Dr0A", OutgoingMessage{ChannelID: "C1", RawText: "new text"}, 0); err != nil {
		t.Fatal(err)
	}
	call := server.CallsFor("drafts.update")[0]
	if call.Params.Get("draft_id") != "Dr0A" {
		t.Errorf("draft_id = %q", call.Params.Get("draft_id"))
	}
	if call.Params.Get("client_last_updated_ts") == "" {
		t.Error("update needs a fresh client_last_updated_ts")
	}
	if call.Params.Get("is_from_composer") != "false" || call.Params.Has("date_scheduled") {
		t.Errorf("editing a plain draft must not schedule it: %v", call.Params)
	}
	if !strings.Contains(call.Params.Get("blocks"), "new text") {
		t.Errorf("blocks should carry the new text: %s", call.Params.Get("blocks"))
	}
}

func TestUpdateDraftScheduled(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.update", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "date_scheduled": float64(777),
		"destinations": []any{map[string]any{"channel_id": "C1"}}}})
	c := browserClient(t, server)

	// Promotion: postAt > 0 flips a plain draft to a scheduled message in place.
	res, err := UpdateDraft(context.Background(), c, "Dr0A", OutgoingMessage{ChannelID: "C1", RawText: "promote me"}, 777)
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "Dr0A" || res.PostAt != 777 {
		t.Errorf("promoted draft = %+v", res)
	}
	call := server.CallsFor("drafts.update")[0]
	if call.Params.Get("is_from_composer") != "true" {
		t.Errorf("promotion must set is_from_composer=true: %v", call.Params)
	}
	if call.Params.Get("date_scheduled") != "777" {
		t.Errorf("date_scheduled = %q", call.Params.Get("date_scheduled"))
	}
	if call.Params.Get("client_last_updated_ts") == "" {
		t.Error("update needs a fresh client_last_updated_ts")
	}
	if !strings.Contains(call.Params.Get("blocks"), "promote me") {
		t.Errorf("blocks should carry the content: %s", call.Params.Get("blocks"))
	}
}

func TestListDraftsPlainOnly(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		map[string]any{"id": "Dr0PLAIN", "date_scheduled": float64(0), "destinations": []any{map[string]any{"channel_id": "C1"}}},
		map[string]any{"id": "Dr0SCHED", "date_scheduled": float64(100), "destinations": []any{map[string]any{"channel_id": "C1"}}},
		map[string]any{"id": "Dr0DEL", "date_scheduled": float64(0), "is_deleted": true, "destinations": []any{map[string]any{"channel_id": "C1"}}},
		map[string]any{"id": "Dr0SENT", "date_scheduled": float64(0), "is_sent": true, "destinations": []any{map[string]any{"channel_id": "C1"}}},
	}})
	c := browserClient(t, server)

	drafts, err := ListDrafts(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].ID != "Dr0PLAIN" {
		t.Errorf("ListDrafts should return only active plain drafts: %+v", drafts)
	}
}

func TestPlainDraftForChannel(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		map[string]any{"id": "Dr0A", "date_scheduled": float64(0), "destinations": []any{map[string]any{"channel_id": "C1"}}},
	}})
	c := browserClient(t, server)

	d, ok, err := PlainDraftForChannel(context.Background(), c, "C1")
	if err != nil || !ok || d.ID != "Dr0A" {
		t.Errorf("match: d=%+v ok=%v err=%v", d, ok, err)
	}
	_, ok, err = PlainDraftForChannel(context.Background(), c, "C2")
	if err != nil || ok {
		t.Errorf("no draft for C2 should be ok=false, nil err: ok=%v err=%v", ok, err)
	}
}

func TestListScheduledWarmsCompletionCacheBrowser(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		map[string]any{"id": "Dr0SCHED", "date_scheduled": float64(1800000000),
			"destinations": []any{map[string]any{"channel_id": "C1"}},
			"blocks":       []any{richTextBlock("browser-scheduled note")}},
	}})
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	cache := NewCache(dir, CacheNormal, DefaultCacheTTL(), func() time.Time { return now })
	c := New(Auth{Type: AuthBrowser, XOXC: "xoxc-test", XOXD: "d", WorkspaceURL: ts.URL},
		WithCache(cache))

	if _, err := ListScheduledMessages(context.Background(), c, ScheduledListOptions{}); err != nil {
		t.Fatal(err)
	}
	// Drafts are browser-only, so this scheduled-draft path is the real-world warm.
	items := ReadCompletions(dir, ts.URL, "Dr0", 10, CompleteScheduled)
	if len(items) != 1 || items[0].Value != "Dr0SCHED" || items[0].Description != "browser-scheduled note" {
		t.Errorf("browser scheduled completions = %+v", items)
	}
}

func TestListScheduledMessagesBrowserFilters(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		map[string]any{"id": "Dr0SCHED", "date_scheduled": float64(100),
			"destinations": []any{map[string]any{"channel_id": "C1"}},
			"blocks":       []any{richTextBlock("the message")}},
		map[string]any{"id": "Dr0PLAIN", "date_scheduled": float64(0),
			"destinations": []any{map[string]any{"channel_id": "C1"}}},
		map[string]any{"id": "Dr0DEL", "date_scheduled": float64(100), "is_deleted": true,
			"destinations": []any{map[string]any{"channel_id": "C1"}}},
		map[string]any{"id": "Dr0SENT", "date_scheduled": float64(100), "is_sent": true,
			"destinations": []any{map[string]any{"channel_id": "C1"}}},
	}})
	c := browserClient(t, server)

	page, err := ListScheduledMessages(context.Background(), c, ScheduledListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.ScheduledMessages) != 1 {
		t.Fatalf("want only the scheduled, not-deleted, not-sent draft; got %d: %+v", len(page.ScheduledMessages), page.ScheduledMessages)
	}
	m := page.ScheduledMessages[0]
	if m["id"] != "Dr0SCHED" || m["channel_id"] != "C1" || m["post_at"] != int64(100) || m["text"] != "the message" {
		t.Errorf("mapped scheduled draft = %+v", m)
	}
}

func TestListScheduledMessagesBrowserChannelFilter(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		map[string]any{"id": "Dr0A", "date_scheduled": float64(1), "destinations": []any{map[string]any{"channel_id": "C1"}}},
		map[string]any{"id": "Dr0B", "date_scheduled": float64(1), "destinations": []any{map[string]any{"channel_id": "C2"}}},
	}})
	c := browserClient(t, server)

	page, err := ListScheduledMessages(context.Background(), c, ScheduledListOptions{ChannelID: "C2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.ScheduledMessages) != 1 || page.ScheduledMessages[0]["id"] != "Dr0B" {
		t.Errorf("channel filter failed: %+v", page.ScheduledMessages)
	}
}

func TestCancelScheduledMessageBrowserDeletesDraft(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.delete", map[string]any{"ok": true})
	c := browserClient(t, server)

	if err := CancelScheduledMessage(context.Background(), c, "ignored-channel", "Dr0X"); err != nil {
		t.Fatal(err)
	}
	if len(server.CallsFor("chat.deleteScheduledMessage")) != 0 {
		t.Error("browser cancel must not call chat.deleteScheduledMessage")
	}
	call := server.CallsFor("drafts.delete")[0]
	if call.Params.Get("draft_id") != "Dr0X" || call.Params.Get("client_last_updated_ts") == "" {
		t.Errorf("drafts.delete params = %v", call.Params)
	}
}

func TestScheduleMessageBrowserCreatesDraft(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0SCH", "date_scheduled": float64(999),
		"destinations": []any{map[string]any{"channel_id": "C1"}}}})
	c := browserClient(t, server)

	res, err := ScheduleMessage(context.Background(), c, OutgoingMessage{ChannelID: "C1", RawText: "hi"}, 999)
	if err != nil {
		t.Fatal(err)
	}
	if res.ScheduledMessageID != "Dr0SCH" || res.PostAt != 999 {
		t.Errorf("schedule result = %+v", res)
	}
	if len(server.CallsFor("chat.scheduleMessage")) != 0 {
		t.Error("browser schedule must not call chat.scheduleMessage")
	}
}
