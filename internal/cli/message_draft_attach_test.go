package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// draft create --attach uploads the file's bytes, finalizes the upload WITHOUT
// a channel (so the id is a real file, not a still-pending one that drafts.create
// rejects with file_not_found), and attaches it by id — keeping the rich_text
// blocks so links/formatting survive. The finalize must not post the file.
func TestDraftCreateAttach(t *testing.T) {
	f := newBrowserCLIFixture(t)

	uploadHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadHost.Close)
	f.server.HandleBody("files.getUploadURLExternal", map[string]any{
		"ok": true, "upload_url": uploadHost.URL + "/u", "file_id": "F0DRAFT1",
	})
	f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	attachment := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(attachment, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "see **attached**", "--attach", attachment); err != nil {
		t.Fatal(err)
	}

	create := f.server.CallsFor("drafts.create")[0]
	if !strings.Contains(create.Params.Get("file_ids"), "F0DRAFT1") {
		t.Errorf("draft file_ids = %q, want the uploaded id", create.Params.Get("file_ids"))
	}
	// The rich_text blocks are kept (formatting survives, unlike a plain-text
	// attachment send).
	if !strings.Contains(create.Params.Get("blocks"), `"bold":true`) {
		t.Errorf("draft should keep formatted blocks: %s", create.Params.Get("blocks"))
	}
	// The upload is finalized exactly once, WITHOUT a channel: that turns the
	// pending upload into a real file (so drafts.create accepts it) without
	// posting it anywhere.
	complete := f.server.CallsFor("files.completeUploadExternal")
	if len(complete) != 1 {
		t.Fatalf("draft attach should finalize the upload once, got %d calls", len(complete))
	}
	if ch := complete[0].Params.Get("channel_id"); ch != "" {
		t.Errorf("draft attach must finalize without a channel (no post), got channel_id=%q", ch)
	}
}

// draftWithFiles builds a plain drafts.list entry carrying file_ids.
func draftWithFiles(id, channelID, text string, fileIDs ...string) map[string]any {
	d := draftObj(id, channelID, text, 0)
	files := make([]any, len(fileIDs))
	for i, fid := range fileIDs {
		files[i] = fid
	}
	d["file_ids"] = files
	return d
}

// get surfaces a draft's file_ids so attachments are visible — from the same
// drafts.list response, no extra call.
func TestDraftGetShowsFileIDs(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftWithFiles("Dr0A", "C12345678", "see attached", "F0A", "F0B")}})

	out, _, err := f.run(t, "message", "draft", "get", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := parseJSON(t, out)["file_ids"].([]any)
	if len(got) != 2 || got[0] != "F0A" || got[1] != "F0B" {
		t.Errorf("get should surface file_ids: %s", out)
	}
}

// Sending a draft that carries attachments goes via files.share (the native
// "send message with files" path) — chat.postMessage can't re-attach an
// already-uploaded file. files.share posts and removes the draft in one call.
func TestDraftSendWithFiles(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftWithFiles("Dr0A", "C12345678", "see attached", "F0A", "F0B")}})
	f.server.HandleBody("files.share", map[string]any{"ok": true, "file_msg_ts": "1.0007"})

	out, _, err := f.run(t, "message", "draft", "send", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["ts"] != "1.0007" {
		t.Errorf("send-with-files should surface the posted ts: %s", out)
	}
	share := f.server.CallsFor("files.share")
	if len(share) != 1 {
		t.Fatalf("draft-with-files send should call files.share once, got %d", len(share))
	}
	if got := share[0].Params.Get("files"); got != "F0A,F0B" {
		t.Errorf("files.share files = %q, want the draft's file ids", got)
	}
	if share[0].Params.Get("draft_id") != "Dr0A" || share[0].Params.Get("channel") != "C12345678" {
		t.Errorf("files.share should carry draft_id + channel: %v", share[0].Params)
	}
	// It must NOT fall back to chat.postMessage (which would drop the files).
	if n := len(f.server.CallsFor("chat.postMessage")); n != 0 {
		t.Errorf("draft-with-files send must not chat.postMessage, got %d", n)
	}
}

// Scheduling a draft that carries attachments re-sends its file_ids through
// drafts.update, so Slack delivers the files when the schedule fires.
func TestDraftSendScheduleKeepsFiles(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftWithFiles("Dr0A", "C12345678", "later with files", "F0A", "F0B")}})
	f.server.HandleBody("drafts.update", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "date_scheduled": float64(1800000000),
		"destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	if _, _, err := f.run(t, "message", "draft", "send", "C12345678", "--schedule-in", "2h"); err != nil {
		t.Fatal(err)
	}
	update := f.server.CallsFor("drafts.update")
	if len(update) != 1 {
		t.Fatalf("scheduled promote should drafts.update once, got %d", len(update))
	}
	for _, fid := range []string{"F0A", "F0B"} {
		if !strings.Contains(update[0].Params.Get("file_ids"), fid) {
			t.Errorf("scheduled promote should keep file id %s: %s", fid, update[0].Params.Get("file_ids"))
		}
	}
}
