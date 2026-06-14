package cli

import (
	"strings"
	"testing"
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
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr0PLAIN", "C12345678", "a plain draft", 0),
		draftObj("Dr0SCHED", "C12345678", "scheduled one", 100), // excluded
	}})

	out, _, err := f.run(t, "message", "draft", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 || lines[0]["id"] != "Dr0PLAIN" || lines[0]["text"] != "a plain draft" {
		t.Errorf("draft list should show only plain drafts: %v", lines)
	}
	if _, has := lines[0]["post_at"]; has {
		t.Error("a plain draft has no post_at")
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
	f.server.HandleBody("drafts.delete", map[string]any{"ok": true})

	out, _, err := f.run(t, "message", "draft", "send", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["ts"] != "1.0001" {
		t.Errorf("out = %s", out)
	}
	// Posts the draft's content, then removes the draft.
	if !strings.Contains(f.server.CallsFor("chat.postMessage")[0].Params.Get("blocks"), "ready to go") {
		t.Error("send should post the draft's blocks")
	}
	if len(f.server.CallsFor("drafts.delete")) != 1 {
		t.Error("send should delete the draft after posting")
	}
}

func TestDraftCreateAlreadyExists(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.create", map[string]any{"ok": false, "error": "attached_draft_exists"})

	_, stderr, err := f.run(t, "message", "draft", "create", "C12345678", "another")
	if err == nil {
		t.Fatal("expected attached_draft_exists to surface")
	}
	p := errPayload(t, stderr)
	if p["fixable_by"] != "agent" || !strings.Contains(p["hint"].(string), "edit") {
		t.Errorf("should hint toward edit/delete: %s", stderr)
	}
}
