package cli

import (
	"net/url"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestMessageEditRequiresYes(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "edit", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "new text")
	if err == nil {
		t.Fatal("expected error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" {
		t.Errorf("payload = %v", payload)
	}
	if !strings.Contains(payload["hint"].(string), "--yes") {
		t.Errorf("hint = %v", payload["hint"])
	}
	if len(f.server.Calls()) != 0 {
		t.Error("no API calls expected without --yes")
	}
}

func TestMessageEditWithYes(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	_, _, err := f.run(t, "message", "edit", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "new *text*", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("chat.update")[0]
	if call.Params.Get("ts") != "1770165109.628379" || call.Params.Get("channel") != "C1A2B3C4D" {
		t.Errorf("params = %v", call.Params)
	}
}

func TestMessageEditDialectAndMentions(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U0ALICEAA", "name": "alice"}}})
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	_, _, err := f.run(t, "message", "edit",
		"https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379",
		"hi @alice in **bold**", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("chat.update")[0]
	// Markdown formatting → rich_text blocks with the resolved mention.
	if blocks := call.Params.Get("blocks"); !strings.Contains(blocks, `"bold":true`) || !strings.Contains(blocks, `"user_id":"U0ALICEAA"`) {
		t.Errorf("edit blocks missing bold/mention: %s", blocks)
	}
	// The text fallback is de-marked plain, with the mention promoted.
	if got := call.Params.Get("text"); got != "hi <@U0ALICEAA> in bold" {
		t.Errorf("edit text fallback = %q", got)
	}
}

const editPermalink = "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379"

// An edit resolves #channel mentions in the new text the same way send/draft/
// forward do (regression: edit previously ran only @-mention resolution).
func TestMessageEditResolvesChannelMention(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleWhen("search.messages",
		func(p url.Values) bool { return p.Get("query") == "in:#general" },
		mockslack.Response{Body: mockslack.SearchMessages(mockslack.ChannelMatch("C0GENERAL"))})
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	if _, _, err := f.run(t, "message", "edit", editPermalink, "join #general now", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := f.server.CallsFor("chat.update")[0].Params.Get("text"); got != "join <#C0GENERAL> now" {
		t.Errorf("text = %q, want resolved channel link", got)
	}
}

// messageWithFiles builds a conversations.history message carrying file ids.
func messageWithFiles(fileIDs ...string) map[string]any {
	msg := simpleMessage("1770165109.628379", "U12345678", "current body")
	var files []any
	for _, id := range fileIDs {
		files = append(files, map[string]any{"id": id, "name": id + ".png", "mimetype": "image/png"})
	}
	msg["files"] = files
	return msg
}

// A text-only edit omits file_ids entirely, so Slack preserves existing
// attachments — and never reads the current message.
func TestMessageEditTextOnlyKeepsAttachments(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	if _, _, err := f.run(t, "message", "edit", editPermalink, "new text", "--yes"); err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("chat.update")[0]
	if _, has := call.Params["file_ids"]; has {
		t.Errorf("text-only edit must not send file_ids (it would replace attachments): %v", call.Params)
	}
	if len(f.server.CallsFor("conversations.history")) != 0 {
		t.Error("text-only edit should not read the current message")
	}
}

// --remove-attachment drops the named id from the message's current file_ids
// (replace-semantics) while keeping the rest.
func TestMessageEditRemoveAttachment(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(messageWithFiles("F1", "F2", "F3")))
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	if _, _, err := f.run(t, "message", "edit", editPermalink, "--remove-attachment", "F2", "--yes"); err != nil {
		t.Fatal(err)
	}
	got := f.server.CallsFor("chat.update")[0].Params.Get("file_ids")
	if got != "F1,F3" {
		t.Errorf("file_ids = %q, want %q (F2 removed, replace-semantics)", got, "F1,F3")
	}
}

// An attachment-only edit re-sends the existing body so it isn't blanked.
func TestMessageEditAttachmentOnlyPreservesBody(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(messageWithFiles("F1", "F2")))
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	if _, _, err := f.run(t, "message", "edit", editPermalink, "--remove-attachment", "F1", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := f.server.CallsFor("chat.update")[0].Params.Get("text"); got != "current body" {
		t.Errorf("attachment-only edit should preserve the existing text, got %q", got)
	}
}

// --attach uploads the file (finalized without a channel) and appends its id to
// the current set.
func TestMessageEditAddAttachment(t *testing.T) {
	f := newCLIFixture(t)
	host := okUploadHost(t)
	f.server.HandleBody("conversations.history", historyWith(messageWithFiles("F1")))
	f.server.HandleBody("files.getUploadURLExternal", map[string]any{
		"ok": true, "upload_url": host.URL + "/u", "file_id": "F0NEW",
	})
	f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	attachment := writeTempFile(t, "extra.txt")
	if _, _, err := f.run(t, "message", "edit", editPermalink, "--attach", attachment, "--yes"); err != nil {
		t.Fatal(err)
	}
	got := f.server.CallsFor("chat.update")[0].Params.Get("file_ids")
	if got != "F1,F0NEW" {
		t.Errorf("file_ids = %q, want existing + uploaded", got)
	}
	// Finalize without a channel: the new file is added to the edited message,
	// not posted on its own.
	if ch := f.server.CallsFor("files.completeUploadExternal")[0].Params.Get("channel_id"); ch != "" {
		t.Errorf("attach must finalize without a channel, got channel_id=%q", ch)
	}
}

// --attach and --remove-attachment in one edit: removals are validated and
// applied first, then the uploaded id is appended (kept…, uploaded…).
func TestMessageEditAddAndRemoveAttachment(t *testing.T) {
	f := newCLIFixture(t)
	host := okUploadHost(t)
	f.server.HandleBody("conversations.history", historyWith(messageWithFiles("F1", "F2", "F3")))
	f.server.HandleBody("files.getUploadURLExternal", map[string]any{
		"ok": true, "upload_url": host.URL + "/u", "file_id": "F0NEW",
	})
	f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	attachment := writeTempFile(t, "extra.txt")
	if _, _, err := f.run(t, "message", "edit", editPermalink,
		"--remove-attachment", "F2", "--attach", attachment, "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := f.server.CallsFor("chat.update")[0].Params.Get("file_ids"); got != "F1,F3,F0NEW" {
		t.Errorf("file_ids = %q, want kept-minus-removed then uploaded", got)
	}
}

// A failed upload during edit aborts before chat.update, so the message's
// attachments are never partially rewritten.
func TestMessageEditAttachUploadFailureAborts(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(messageWithFiles("F1")))
	f.server.HandleBody("files.getUploadURLExternal", map[string]any{"ok": false, "error": "upload_disabled"})
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	attachment := writeTempFile(t, "extra.txt")
	if _, _, err := f.run(t, "message", "edit", editPermalink, "--attach", attachment, "--yes"); err == nil {
		t.Fatal("a failed upload should abort the edit")
	}
	if n := len(f.server.CallsFor("chat.update")); n != 0 {
		t.Errorf("chat.update must not run when an upload fails, got %d calls", n)
	}
}

// Removal ids are whitespace-trimmed, so a padded id still matches.
func TestMessageEditRemoveAttachmentTrimsWhitespace(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(messageWithFiles("F1", "F2")))
	f.server.HandleBody("chat.update", map[string]any{"ok": true})

	if _, _, err := f.run(t, "message", "edit", editPermalink, "--remove-attachment", "  F2  ", "--yes"); err != nil {
		t.Fatal(err)
	}
	if got := f.server.CallsFor("chat.update")[0].Params.Get("file_ids"); got != "F1" {
		t.Errorf("file_ids = %q, want F1 (padded id should still match F2)", got)
	}
}

// Removing an id the message doesn't have fails loudly (agent-fixable) rather
// than silently no-op'ing.
func TestMessageEditRemoveUnknownAttachment(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(messageWithFiles("F1")))

	_, stderr, err := f.run(t, "message", "edit", editPermalink, "--remove-attachment", "FZZZ", "--yes")
	if err == nil {
		t.Fatal("expected error for an unknown attachment id")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("stderr = %s", stderr)
	}
	if len(f.server.CallsFor("chat.update")) != 0 {
		t.Error("no chat.update expected when the removal id is invalid")
	}
}

// An edit with neither new text nor attachment flags is rejected before any
// API call.
func TestMessageEditNothingToDo(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "edit", editPermalink, "--yes")
	if err == nil {
		t.Fatal("expected error")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("stderr = %s", stderr)
	}
	if len(f.server.Calls()) != 0 {
		t.Error("no API calls expected for an empty edit")
	}
}

func TestMessageDeleteWithYes(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.delete", map[string]any{"ok": true})

	_, _, err := f.run(t, "message", "delete", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.server.CallsFor("chat.delete")) != 1 {
		t.Error("chat.delete not called")
	}
}

func TestMessageReactAddUnicode(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("reactions.add", map[string]any{"ok": true})

	_, _, err := f.run(t, "message", "react", "add", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "🚀")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.server.CallsFor("reactions.add")[0].Params.Get("name"); got != "rocket" {
		t.Errorf("name = %q", got)
	}
}
