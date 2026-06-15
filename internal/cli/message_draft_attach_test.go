package cli

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// okUploadHost is an httptest server that accepts the raw byte POST (200).
func okUploadHost(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)
	return srv
}

func writeTempFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

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

// R13: the parallel multi-file path uploads both files, finalizes them in ONE
// no-channel completion, and attaches both ids to the draft.
func TestDraftCreateAttachMultiple(t *testing.T) {
	f := newBrowserCLIFixture(t)
	uploadHost := okUploadHost(t)
	f.server.HandleWhen("files.getUploadURLExternal",
		func(p url.Values) bool { return p.Get("filename") == "a.txt" },
		mockslack.Response{Body: map[string]any{"ok": true, "upload_url": uploadHost.URL + "/u", "file_id": "F0AAAA"}})
	f.server.HandleWhen("files.getUploadURLExternal",
		func(p url.Values) bool { return p.Get("filename") == "b.txt" },
		mockslack.Response{Body: map[string]any{"ok": true, "upload_url": uploadHost.URL + "/u", "file_id": "F0BBBB"}})
	f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	a, b := writeTempFile(t, "a.txt"), writeTempFile(t, "b.txt")
	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "two files", "--attach", a, "--attach", b); err != nil {
		t.Fatal(err)
	}
	complete := f.server.CallsFor("files.completeUploadExternal")
	if len(complete) != 1 || complete[0].Params.Get("channel_id") != "" {
		t.Fatalf("want one no-channel completion, got %d (channel_id=%q)", len(complete), firstParamChannel(complete))
	}
	if files := complete[0].Params.Get("files"); !strings.Contains(files, `"id":"F0AAAA"`) || !strings.Contains(files, `"id":"F0BBBB"`) {
		t.Errorf("completion files = %q, want both ids", files)
	}
	if ids := f.server.CallsFor("drafts.create")[0].Params.Get("file_ids"); !strings.Contains(ids, "F0AAAA") || !strings.Contains(ids, "F0BBBB") {
		t.Errorf("draft file_ids = %q, want both", ids)
	}
}

// R13: if any one upload fails, the whole draft create aborts — no partial draft.
func TestDraftCreateAttachOneFails(t *testing.T) {
	f := newBrowserCLIFixture(t)
	uploadHost := okUploadHost(t)
	f.server.HandleWhen("files.getUploadURLExternal",
		func(p url.Values) bool { return p.Get("filename") == "ok.txt" },
		mockslack.Response{Body: map[string]any{"ok": true, "upload_url": uploadHost.URL + "/u", "file_id": "F0OK"}})
	f.server.HandleWhen("files.getUploadURLExternal",
		func(p url.Values) bool { return p.Get("filename") == "bad.txt" },
		mockslack.Response{Body: map[string]any{"ok": false, "error": "upload_disabled"}})
	f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	good, bad := writeTempFile(t, "ok.txt"), writeTempFile(t, "bad.txt")
	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "two", "--attach", good, "--attach", bad); err == nil {
		t.Fatal("a failed upload should abort the draft create")
	}
	if n := len(f.server.CallsFor("drafts.create")); n != 0 {
		t.Errorf("no draft must be created when an upload fails, got %d", n)
	}
}

// R14: a non-2xx response to the raw byte POST is a (retryable) error, and the
// upload is not finalized.
func TestDraftCreateAttachByteUploadHTTPError(t *testing.T) {
	f := newBrowserCLIFixture(t)
	failHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(failHost.Close)
	f.server.HandleBody("files.getUploadURLExternal", map[string]any{"ok": true, "upload_url": failHost.URL + "/u", "file_id": "F0X"})
	f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0A", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "x", "--attach", writeTempFile(t, "f.txt")); err == nil {
		t.Fatal("a non-2xx byte upload should error")
	}
	if n := len(f.server.CallsFor("files.completeUploadExternal")); n != 0 {
		t.Errorf("must not finalize after a failed byte upload, got %d", n)
	}
}

// R16: a non-regular --attach path (a directory) is rejected before any upload.
func TestDraftCreateAttachNonRegularFile(t *testing.T) {
	f := newBrowserCLIFixture(t)
	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "x", "--attach", t.TempDir()); err == nil {
		t.Fatal("attaching a directory should error")
	}
	if n := len(f.server.CallsFor("files.getUploadURLExternal")); n != 0 {
		t.Errorf("a non-regular file must be rejected before upload, got %d", n)
	}
}

// R15: a draft with files whose files.share fails surfaces an error and does NOT
// fall back to chat.postMessage (which would drop the attachments).
func TestDraftSendShareFails(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftWithFiles("Dr0A", "C12345678", "see attached", "F0A")}})
	f.server.HandleBody("files.share", map[string]any{"ok": false, "error": "file_not_found"})

	if _, _, err := f.run(t, "message", "draft", "send", "Dr0A"); err == nil {
		t.Fatal("files.share failure should surface as an error")
	}
	if n := len(f.server.CallsFor("chat.postMessage")); n != 0 {
		t.Errorf("a file draft must not fall back to chat.postMessage, got %d", n)
	}
}

func firstParamChannel(calls []mockslack.Call) string {
	if len(calls) == 0 {
		return ""
	}
	return calls[0].Params.Get("channel_id")
}
