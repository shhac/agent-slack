package cli

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// draftObj builds a drafts.list entry. postAt 0 → plain draft.
func draftObj(id, channelID, text string, postAt int) map[string]any {
	return map[string]any{
		"id":             id,
		"date_scheduled": float64(postAt),
		"destinations":   []any{map[string]any{"channel_id": channelID}},
		"blocks": []any{map[string]any{"type": "rich_text", "elements": []any{
			map[string]any{"type": "rich_text_section", "elements": []any{
				map[string]any{"type": "text", "text": text}}}}}},
	}
}

func TestDraftListPlainOnly(t *testing.T) {
	f := newBrowserCLIFixture(t)
	// Our drafts are is_from_composer now, so they're indistinguishable from a
	// draft the user started in-app — both are listed. Only scheduled (and
	// deleted/sent) drafts are filtered out.
	composer := draftObj("Dr0COMPOSER", "C12345678", "also a draft", 0)
	composer["is_from_composer"] = true
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0PLAIN", "C12345678", "a plain draft", 0),
		draftObj("Dr0SCHED", "C12345678", "scheduled one", 100), // excluded
		composer,
	}})

	out, _, err := f.run(t, "message", "draft", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	ids := map[string]bool{}
	for _, l := range lines {
		ids[l["id"].(string)] = true
	}
	if len(lines) != 2 || !ids["Dr0PLAIN"] || !ids["Dr0COMPOSER"] {
		t.Errorf("draft list should show both unscheduled drafts, not the scheduled one: %v", lines)
	}
}

func TestDraftGet(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "for this channel", 0),
		draftObj("Dr0B", "C87654321", "other channel", 0),
	}})

	out, _, err := f.run(t, "message", "draft", "get", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	got := parseJSON(t, out)
	if got["id"] != "Dr0A" || got["text"] != "for this channel" {
		t.Errorf("get returned the wrong draft: %v", got)
	}
}

func TestDraftGetNoDraft(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{}})

	_, stderr, err := f.run(t, "message", "draft", "get", "C12345678")
	if err == nil {
		t.Fatal("expected a no-draft error")
	}
	p := errPayload(t, stderr)
	if p["fixable_by"] != "agent" || !strings.Contains(p["hint"].(string), "create") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestDraftEdit(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "old text", 0)}})
	f.server.HandleBody("drafts.update", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	out, _, err := f.run(t, "message", "draft", "edit", "C12345678", "new text")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["draft_id"] != "Dr0A" {
		t.Errorf("out = %s", out)
	}
	call := f.server.CallsFor("drafts.update")[0]
	if call.Params.Get("draft_id") != "Dr0A" || !strings.Contains(call.Params.Get("blocks"), "new text") {
		t.Errorf("update should replace the draft's blocks: %v", call.Params)
	}
}

func TestDraftDeleteRequiresYes(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "x", 0)}})

	_, stderr, err := f.run(t, "message", "draft", "delete", "C12345678")
	if err == nil {
		t.Fatal("delete should require --yes")
	}
	if errPayload(t, stderr)["fixable_by"] != "human" {
		t.Errorf("stderr = %s", stderr)
	}

	f.server.HandleBody("drafts.delete", map[string]any{"ok": true})
	out, _, err := f.run(t, "message", "draft", "delete", "C12345678", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["draft_id"] != "Dr0A" {
		t.Errorf("out = %s", out)
	}
	if f.server.CallsFor("drafts.delete")[0].Params.Get("draft_id") != "Dr0A" {
		t.Error("delete should target the draft id")
	}
}

func TestDraftSend(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "ready to go", 0)}})
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.0001", "channel": "C12345678"})

	out, _, err := f.run(t, "message", "draft", "send", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	sent := parseJSON(t, out)
	if sent["ts"] != "1.0001" {
		t.Errorf("out = %s", out)
	}
	if perma, _ := sent["permalink"].(string); !strings.Contains(perma, "/archives/C12345678/p") {
		t.Errorf("send should surface a permalink: %v", sent["permalink"])
	}
	// Posts the draft's content, passing draft_id so Slack removes the draft as
	// part of the post (native, atomic — no separate drafts.delete to race).
	post := f.server.CallsFor("chat.postMessage")[0]
	if !strings.Contains(post.Params.Get("blocks"), "ready to go") {
		t.Error("send should post the draft's blocks")
	}
	if post.Params.Get("draft_id") != "Dr0A" {
		t.Errorf("send should post with the draft_id so Slack clears it: %q", post.Params.Get("draft_id"))
	}
	if n := len(f.server.CallsFor("drafts.delete")); n != 0 {
		t.Errorf("send should not issue a separate drafts.delete (draft_id clears it), got %d", n)
	}
}

func TestDraftCreateSlackMarkdown(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	// --slack-markdown: single *bold* is bold, and **double** stays literal.
	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "a *bold* and **lit**", "--slack-markdown"); err != nil {
		t.Fatal(err)
	}
	blocks := f.server.CallsFor("drafts.create")[0].Params.Get("blocks")
	if !strings.Contains(blocks, `"bold":true`) || !strings.Contains(blocks, "bold") {
		t.Errorf("slack-markdown draft should bold single-*: %s", blocks)
	}
}

func TestDraftSendSchedulePromotes(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "later, please", 0)}})
	f.server.HandleBody("drafts.update", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "date_scheduled": float64(1800000000),
		"destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	out, _, err := f.run(t, "message", "draft", "send", "C12345678", "--schedule-in", "2h")
	if err != nil {
		t.Fatal(err)
	}
	got := parseJSON(t, out)
	// post_at comes from the API's echoed date_scheduled, not the requested time.
	if got["scheduled_message_id"] != "Dr0A" || got["post_at"].(float64) != 1800000000 {
		t.Errorf("promotion payload = %v", got)
	}
	// Promotion edits the draft in place — it must NOT post or delete it.
	if len(f.server.CallsFor("chat.postMessage")) != 0 || len(f.server.CallsFor("drafts.delete")) != 0 {
		t.Error("scheduling a draft must promote in place, not post-and-delete")
	}
	call := f.server.CallsFor("drafts.update")[0]
	if call.Params.Get("draft_id") != "Dr0A" || call.Params.Get("date_scheduled") == "" {
		t.Errorf("update should schedule the draft: %v", call.Params)
	}
	if call.Params.Get("is_from_composer") != "true" {
		t.Errorf("a promoted draft must be is_from_composer=true: %v", call.Params)
	}
}

func TestDraftSendScheduleFallsBackToRequestedTime(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "later", 0)}})
	// The update echo omits date_scheduled — the CLI must report the requested time.
	f.server.HandleBody("drafts.update", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	when := time.Now().Add(48 * time.Hour).Unix()
	out, _, err := f.run(t, "message", "draft", "send", "C12345678", "--schedule", strconv.FormatInt(when, 10))
	if err != nil {
		t.Fatal(err)
	}
	got := parseJSON(t, out)
	if int64(got["post_at"].(float64)) != when {
		t.Errorf("omitted date_scheduled should fall back to the requested time: got %v want %d", got["post_at"], when)
	}
	if got["scheduled_message_id"] != "Dr0A" {
		t.Errorf("payload = %v", got)
	}
}

func TestDraftCreateResolvesHandleAndMarkdown(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"}}})
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "hi @alice in **bold**"); err != nil {
		t.Fatal(err)
	}
	blocks := f.server.CallsFor("drafts.create")[0].Params.Get("blocks")
	if !strings.Contains(blocks, `"user_id":"U0ALICEAA"`) {
		t.Errorf("draft should carry the resolved @alice mention: %s", blocks)
	}
	if !strings.Contains(blocks, `"bold":true`) {
		t.Errorf("draft should carry Markdown bold: %s", blocks)
	}
}

// A draft id (Dr…) addresses one draft directly, bypassing target resolution.
func TestDraftSendByID(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "by id", 0)}})
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.1", "channel": "C12345678"})

	out, _, err := f.run(t, "message", "draft", "send", "Dr0A")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["ts"] != "1.1" {
		t.Errorf("send by id should post that draft: %s", out)
	}
	if f.server.CallsFor("chat.postMessage")[0].Params.Get("draft_id") != "Dr0A" {
		t.Error("send by id should post the addressed draft")
	}
}

func TestDraftEditByID(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "old", 0)}})
	f.server.HandleBody("drafts.update", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	out, _, err := f.run(t, "message", "draft", "edit", "Dr0A", "new content")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["draft_id"] != "Dr0A" {
		t.Errorf("edit by id should return that draft: %s", out)
	}
	upd := f.server.CallsFor("drafts.update")[0]
	if upd.Params.Get("draft_id") != "Dr0A" || !strings.Contains(upd.Params.Get("blocks"), "new content") {
		t.Errorf("edit by id should update that draft with the new text: %v", upd.Params)
	}
}

// When a target holds more than one draft, resolution refuses to guess: it
// errors and lists the candidate ids so the caller can pass one.
func TestDraftResolveAmbiguousTarget(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0A", "C12345678", "first", 0),
		draftObj("Dr0B", "C12345678", "second", 0)}})

	_, stderr, err := f.run(t, "message", "draft", "get", "C12345678")
	if err == nil {
		t.Fatal("an ambiguous target should error rather than pick one")
	}
	p := errPayload(t, stderr)
	if msg, _ := p["error"].(string); !strings.Contains(msg, "Dr0A") || !strings.Contains(msg, "Dr0B") {
		t.Errorf("error should list the candidate ids: %s", stderr)
	}
	if hint, _ := p["hint"].(string); !strings.Contains(hint, "id") {
		t.Errorf("hint should suggest passing an id: %s", stderr)
	}
}

// Drafts are is_from_composer (non-intrusive, many-per-target), so create never
// conflicts — it returns a fresh id even when the target already has drafts.
func TestDraftCreateReturnsID(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0NEW", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	out, _, err := f.run(t, "message", "draft", "create", "C12345678", "another")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["draft_id"] != "Dr0NEW" {
		t.Errorf("create should return the new draft id: %s", out)
	}
	if got := f.server.CallsFor("drafts.create")[0].Params.Get("is_from_composer"); got != "true" {
		t.Errorf("draft should be created is_from_composer=true, got %q", got)
	}
}
