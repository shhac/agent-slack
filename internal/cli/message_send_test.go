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

func TestMessageSendMarkdownBoldBecomesBlocks(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "C123"})

	if _, _, err := f.run(t, "message", "send", "#general", "this is **bold** now"); err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("chat.postMessage")[0]
	// Standard Markdown formatting must live in rich_text blocks, since the text
	// field would show literal asterisks.
	if blocks := call.Params.Get("blocks"); !strings.Contains(blocks, `"bold":true`) {
		t.Errorf("expected bold rich_text block, got %q", blocks)
	}
	// The notification/text fallback is de-marked plain, not raw markdown.
	if got := call.Params.Get("text"); got != "this is bold now" {
		t.Errorf("text fallback = %q, want de-marked plain", got)
	}
}

func TestMessageSendResolvesHandleMention(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"},
	}})
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.0", "channel": "C123"})

	if _, _, err := f.run(t, "message", "send", "#general", "hi @alice"); err != nil {
		t.Fatal(err)
	}
	// @alice resolved to a real user mention token in the text field.
	if got := f.server.CallsFor("chat.postMessage")[0].Params.Get("text"); got != "hi <@U0ALICEAA>" {
		t.Errorf("text = %q, want resolved mention", got)
	}
}

func TestMessageSendSlackMarkdownKeepsTextField(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "C123"})

	if _, _, err := f.run(t, "message", "send", "#general", "this is *bold* now", "--slack-markdown"); err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("chat.postMessage")[0]
	// Slack mrkdwn renders in the text field, so inline formatting needs no blocks.
	if call.Params.Has("blocks") {
		t.Error("slack-markdown inline formatting should not force blocks")
	}
	if got := call.Params.Get("text"); got != "this is *bold* now" {
		t.Errorf("text = %q, want the Slack mrkdwn verbatim", got)
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

func TestMessageDraftBrowser(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0DRAFT", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	out, _, err := f.run(t, "message", "draft", "create", "C12345678", "hand-off text")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["draft_id"] != "Dr0DRAFT" || payload["ok"] != true {
		t.Errorf("out = %s", out)
	}
	call := f.server.CallsFor("drafts.create")[0]
	if call.Params.Get("is_from_composer") != "false" || call.Params.Has("date_scheduled") {
		t.Errorf("a plain draft is not scheduled: %v", call.Params)
	}
	if !strings.Contains(call.Params.Get("blocks"), "hand-off text") {
		t.Errorf("draft must carry the text as rich_text: %s", call.Params.Get("blocks"))
	}
}

func TestMessageDraftBlocksFile(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{"id": "Dr0BK"}})
	blocksFile := filepath.Join(t.TempDir(), "blocks.json")
	if err := os.WriteFile(blocksFile, []byte(`[{"type":"section","text":{"type":"mrkdwn","text":"from block kit"}}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	// --blocks passes the supplied Block Kit through verbatim (no text arg).
	if _, _, err := f.run(t, "message", "draft", "create", "C12345678", "--blocks", blocksFile); err != nil {
		t.Fatal(err)
	}
	if got := f.server.CallsFor("drafts.create")[0].Params.Get("blocks"); !strings.Contains(got, "from block kit") {
		t.Errorf("--blocks should pass through: %s", got)
	}
}

func TestMessageDraftRequiresBrowserAuth(t *testing.T) {
	f := newCLIFixture(t) // standard (bot) auth
	_, stderr, err := f.run(t, "message", "draft", "create", "C12345678", "hi")
	if err == nil {
		t.Fatal("expected error: drafts need browser auth")
	}
	if errPayload(t, stderr)["fixable_by"] != "human" {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestMessageScheduledCancelBrowserNoChannel(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.delete", map[string]any{"ok": true})

	// Browser auth cancels by the globally-unique draft id; no --channel needed.
	out, _, err := f.run(t, "message", "scheduled", "cancel", "Dr0X", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["ok"] != true {
		t.Errorf("out = %s", out)
	}
	if f.server.CallsFor("drafts.delete")[0].Params.Get("draft_id") != "Dr0X" {
		t.Error("cancel should delete the draft by id")
	}
}

func TestMessageSendScheduledBrowserCreatesDraft(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
		"id": "Dr0SCHED", "date_scheduled": float64(9999999999),
		"destinations": []any{map[string]any{"channel_id": "C12345678"}}}})

	out, _, err := f.run(t, "message", "send", "C12345678", "later", "--schedule-in", "2d")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["scheduled_message_id"] != "Dr0SCHED" {
		t.Errorf("out = %s", out)
	}
	if len(f.server.CallsFor("chat.scheduleMessage")) != 0 {
		t.Error("browser scheduled send must not call chat.scheduleMessage")
	}
	if f.server.CallsFor("drafts.create")[0].Params.Get("is_from_composer") != "true" {
		t.Error("scheduled draft must be a composer draft")
	}
}
