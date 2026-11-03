package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes is the smallest valid PNG header http.DetectContentType recognises.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func imageFixture(t *testing.T, newFixture func(*testing.T) *cliFixture) (*cliFixture, string) {
	t.Helper()
	f := newFixture(t)
	uploadHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadHost.Close)
	f.server.HandleBody("files.getUploadURLExternal", map[string]any{
		"ok": true, "upload_url": uploadHost.URL + "/upload", "file_id": "F0IMAGE001",
	})
	f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})
	f.server.HandleBody("files.info", map[string]any{"ok": true, "file": map[string]any{"id": "F0IMAGE001", "original_w": 360}})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chart.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.txt"), []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, dir
}

func TestMarkdownImageUploadsAndPlacesTheFile(t *testing.T) {
	f, dir := imageFixture(t, newCLIFixture)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "C123"})

	text := "Latency\n\n![latency chart](" + filepath.Join(dir, "chart.png") + ")\n\nand again\n\n![same](" + filepath.Join(dir, "chart.png") + ")"
	if _, _, err := f.run(t, "message", "send", "#general", text); err != nil {
		t.Fatal(err)
	}

	// The same file twice is one upload, finalized without a channel so it is
	// shared only by the block that places it.
	if n := len(f.server.CallsFor("files.getUploadURLExternal")); n != 1 {
		t.Errorf("uploads = %d, want 1 for a repeated path", n)
	}
	if complete := f.server.CallsFor("files.completeUploadExternal")[0]; complete.Params.Has("channel_id") {
		t.Errorf("the upload must not post on its own: %v", complete.Params)
	}
	blocks := f.server.CallsFor("chat.postMessage")[0].Params.Get("blocks")
	if strings.Count(blocks, `"slack_file":{"id":"F0IMAGE001"}`) != 2 || !strings.Contains(blocks, `"alt_text":"latency chart"`) {
		t.Errorf("blocks = %s", blocks)
	}
	if strings.Contains(blocks, dir) {
		t.Errorf("the local path leaked onto the wire: %s", blocks)
	}
}

func TestMarkdownImageRefusesNonImages(t *testing.T) {
	f, dir := imageFixture(t, newCLIFixture)
	f.resolvableChannel("C123")

	_, stderr, err := f.run(t, "message", "send", "#general", "![x]("+filepath.Join(dir, "secrets.txt")+")")
	if err == nil || !strings.Contains(stderr, "is not an image") {
		t.Fatalf("want a not-an-image error, got err=%v stderr=%s", err, stderr)
	}
	if n := len(f.server.CallsFor("files.getUploadURLExternal")); n != 0 {
		t.Errorf("a non-image must not be uploaded (%d upload calls)", n)
	}

	_, stderr, err = f.run(t, "message", "send", "#general", "![x]("+filepath.Join(dir, "missing.png")+")")
	if err == nil || !strings.Contains(stderr, `"fixable_by":"agent"`) {
		t.Errorf("a missing image should be agent-fixable: err=%v stderr=%s", err, stderr)
	}
}

func TestMarkdownImageInDraft(t *testing.T) {
	f, dir := imageFixture(t, newBrowserCLIFixture)
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0DRAFT", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "![chart]("+filepath.Join(dir, "chart.png")+")"); err != nil {
		t.Fatal(err)
	}
	if blocks := f.server.CallsFor("drafts.create")[0].Params.Get("blocks"); !strings.Contains(blocks, `"slack_file":{"id":"F0IMAGE001"}`) {
		t.Errorf("draft blocks = %s", blocks)
	}
}
