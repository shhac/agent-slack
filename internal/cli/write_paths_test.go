package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMessageSendAttach covers the external-upload flow end to end:
// files.getUploadURLExternal → raw POST of the bytes → completeUploadExternal.
func TestMessageSendAttach(t *testing.T) {
	f := newCLIFixture(t)

	var uploaded []byte
	uploadHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		uploaded = body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadHost.Close)

	f.resolvableChannel("C12345678")
	f.server.HandleBody("files.getUploadURLExternal", map[string]any{
		"ok": true, "upload_url": uploadHost.URL + "/upload", "file_id": "F0UPLOAD1",
	})
	f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})

	attachment := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(attachment, []byte("attachment bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := f.run(t, "message", "send", "#general", "see attached", "--attach", attachment, "--thread-ts", "1.000001")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["ok"] != true {
		t.Errorf("out = %s", out)
	}
	if string(uploaded) != "attachment bytes" {
		t.Errorf("uploaded = %q", uploaded)
	}

	initCall := f.server.CallsFor("files.getUploadURLExternal")[0]
	if initCall.Params.Get("filename") != "notes.txt" || initCall.Params.Get("length") != "16" {
		t.Errorf("init params = %v", initCall.Params)
	}
	complete := f.server.CallsFor("files.completeUploadExternal")[0]
	if complete.Params.Get("channel_id") != "C12345678" || complete.Params.Get("thread_ts") != "1.000001" {
		t.Errorf("complete params = %v", complete.Params)
	}
	if !strings.Contains(complete.Params.Get("files"), `"id":"F0UPLOAD1"`) {
		t.Errorf("files param = %q", complete.Params.Get("files"))
	}
	if complete.Params.Get("initial_comment") != "see attached" {
		t.Errorf("initial_comment = %q", complete.Params.Get("initial_comment"))
	}
	if len(f.server.CallsFor("chat.postMessage")) != 0 {
		t.Error("attach sends must not also chat.postMessage")
	}
}

func TestMessageSendAttachMissingFile(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C12345678")
	_, stderr, err := f.run(t, "message", "send", "#general", "x", "--attach", "/no/such/file.txt")
	if err == nil {
		t.Fatal("expected error")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestLaterSaveAndRemove(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("saved.add", map[string]any{"ok": true})
	f.server.HandleBody("saved.delete", map[string]any{"ok": true})
	permalink := "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379"

	if _, _, err := f.run(t, "later", "save", permalink); err != nil {
		t.Fatal(err)
	}
	add := f.server.CallsFor("saved.add")[0]
	if add.Params.Get("item_id") != "C1A2B3C4D" || add.Params.Get("ts") != "1770165109.628379" || add.Params.Get("item_type") != "message" {
		t.Errorf("saved.add params = %v", add.Params)
	}

	if _, _, err := f.run(t, "later", "remove", permalink); err != nil {
		t.Fatal(err)
	}
	del := f.server.CallsFor("saved.delete")[0]
	if del.Params.Get("item_id") != "C1A2B3C4D" || del.Params.Get("ts") != "1770165109.628379" {
		t.Errorf("saved.delete params = %v", del.Params)
	}
}

func TestLoadBlocksFromPath(t *testing.T) {
	// stdin
	blocks, err := loadBlocksFromPath(strings.NewReader(`[{"type":"section"}]`), "-")
	if err != nil || len(blocks) != 1 {
		t.Errorf("stdin: blocks = %v, err = %v", blocks, err)
	}
	// file
	path := filepath.Join(t.TempDir(), "blocks.json")
	if err := os.WriteFile(path, []byte(`[{"type":"divider"},{"type":"section"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	blocks, err = loadBlocksFromPath(nil, path)
	if err != nil || len(blocks) != 2 {
		t.Errorf("file: blocks = %v, err = %v", blocks, err)
	}
	// error cases
	for name, input := range map[string]string{
		"not an array":      `{"type":"section"}`,
		"non-object member": `[{"type":"section"}, 42]`,
		"invalid json":      `{`,
	} {
		if _, err := loadBlocksFromPath(strings.NewReader(input), "-"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := loadBlocksFromPath(nil, "/no/such/blocks.json"); err == nil {
		t.Error("missing file: expected error")
	}
}
