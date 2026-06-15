package cli

import (
	"strings"
	"testing"
)

const acmePermalink = "https://acme.slack.com/archives/C0SOURCE00/p1700000000000100"

func TestMessageSendForward(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.0", "channel": "C0DEST0001"})

	if _, _, err := f.run(t, "message", "send", "C0DEST0001", "--forward", acmePermalink); err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("chat.postMessage")[0]
	// The permalink rides in the text so Slack unfurls it into a shared card,
	// and unfurling is forced on (bot tokens default it off).
	if got := call.Params.Get("text"); !strings.Contains(got, acmePermalink) {
		t.Errorf("text = %q, want the permalink", got)
	}
	if got := call.Params.Get("unfurl_links"); got != "true" {
		t.Errorf("unfurl_links = %q, want true", got)
	}
}

func TestMessageSendForwardWithComment(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.0", "channel": "C0DEST0001"})

	if _, _, err := f.run(t, "message", "send", "C0DEST0001", "worth a read", "--forward", acmePermalink); err != nil {
		t.Fatal(err)
	}
	got := f.server.CallsFor("chat.postMessage")[0].Params.Get("text")
	if !strings.Contains(got, "worth a read") || !strings.Contains(got, acmePermalink) {
		t.Errorf("text = %q, want comment + permalink", got)
	}
}

// A permalink in a different workspace is a link, not a forward — rejected.
func TestMessageSendForwardCrossWorkspaceRejected(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.0", "channel": "C0DEST0001"})

	out, stderr, err := f.run(t, "message", "send", "C0DEST0001", "--forward",
		"https://other-team.slack.com/archives/C0SOURCE00/p1700000000000100")
	if err == nil {
		t.Fatalf("cross-workspace forward should error; out=%q", out)
	}
	if errPayload(t, stderr)["fixable_by"] == nil {
		t.Errorf("expected a structured error: %s", stderr)
	}
	if n := len(f.server.CallsFor("chat.postMessage")); n != 0 {
		t.Errorf("nothing should be posted on a rejected forward, got %d calls", n)
	}
}

func TestMessageSendForwardRejectsBlocks(t *testing.T) {
	f := newCLIFixture(t)
	out, _, err := f.run(t, "message", "send", "C0DEST0001", "--forward", acmePermalink, "--blocks", "-")
	if err == nil {
		t.Fatalf("--forward with --blocks should error; out=%q", out)
	}
}

func TestMessageSendForwardInvalidPermalink(t *testing.T) {
	f := newCLIFixture(t)
	out, _, err := f.run(t, "message", "send", "C0DEST0001", "--forward", "not-a-url")
	if err == nil {
		t.Fatalf("a non-permalink --forward should error; out=%q", out)
	}
}
