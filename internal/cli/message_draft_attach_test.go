package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// draft create --attach uploads the file's bytes and attaches it by id —
// keeping the rich_text blocks (so links/formatting survive) — without posting
// (no completeUploadExternal).
func TestDraftCreateAttach(t *testing.T) {
	f := newBrowserCLIFixture(t)

	uploadHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadHost.Close)
	f.server.HandleBody("files.getUploadURLExternal", map[string]any{
		"ok": true, "upload_url": uploadHost.URL + "/u", "file_id": "F0DRAFT1",
	})
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
	// A draft references the file id directly — it must not be posted.
	if n := len(f.server.CallsFor("files.completeUploadExternal")); n != 0 {
		t.Errorf("draft attach must not completeUploadExternal (no post), got %d", n)
	}
}
