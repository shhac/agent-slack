package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageSendToChannel(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1770165109.628379", "channel": "C123"})

	out, _, err := f.run(t, "message", "send", "#general", "@U05BRPTKL6A check this & that")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["ok"] != true || payload["ts"] != "1770165109.628379" {
		t.Errorf("payload = %v", payload)
	}
	if payload["permalink"] != "https://acme.slack.com/archives/C123/p1770165109628379" {
		t.Errorf("permalink = %v", payload["permalink"])
	}
	call := f.server.CallsFor("chat.postMessage")[0]
	if got := call.Params.Get("text"); got != "<@U05BRPTKL6A> check this &amp; that" {
		t.Errorf("text = %q (outbound formatting)", got)
	}
	if call.Params.Has("blocks") {
		t.Error("plain text should not send rich_text blocks")
	}
}

func TestMessageSendListBecomesRichText(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "C123"})

	if _, _, err := f.run(t, "message", "send", "#general", "Plan:\n- one\n- two"); err != nil {
		t.Fatal(err)
	}
	blocks := f.server.CallsFor("chat.postMessage")[0].Params.Get("blocks")
	if !strings.Contains(blocks, "rich_text_list") {
		t.Errorf("blocks = %q, want rich_text_list", blocks)
	}
}

func TestMessageSendDMTarget(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.open", map[string]any{"ok": true, "channel": map[string]any{"id": "D999"}})
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "D999"})

	out, _, err := f.run(t, "message", "send", "U12345ABCDE", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["channel_id"] != "D999" {
		t.Errorf("out = %s", out)
	}
}

func TestMessageSendHandleTarget(t *testing.T) {
	f := newCLIFixture(t)
	// @handle resolves to an id (users.list), then a DM opens.
	f.server.HandleBody("users.list", map[string]any{
		"ok": true, "members": []any{map[string]any{"id": "U12345ABCDE", "name": "alice"}},
	})
	f.server.HandleBody("conversations.open", map[string]any{"ok": true, "channel": map[string]any{"id": "D999"}})
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "D999"})

	out, _, err := f.run(t, "message", "send", "@alice", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["channel_id"] != "D999" {
		t.Errorf("@handle target should DM the resolved user: %s", out)
	}
	if got := f.server.CallsFor("conversations.open")[0].Params.Get("users"); got != "U12345ABCDE" {
		t.Errorf("opened DM with %q, want the resolved id", got)
	}
}

func TestMessageSendScheduled(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.scheduleMessage", map[string]any{
		"ok": true, "channel": "C123", "scheduled_message_id": "Q123", "post_at": float64(9999999999),
	})

	out, _, err := f.run(t, "message", "send", "#general", "later", "--schedule-in", "3h")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["scheduled_message_id"] != "Q123" {
		t.Errorf("payload = %v", payload)
	}
	if len(f.server.CallsFor("chat.postMessage")) != 0 {
		t.Error("scheduled send must not call chat.postMessage")
	}
}

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
