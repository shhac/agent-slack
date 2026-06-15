package slack

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

const srcPermalink = "https://acme.slack.com/archives/C0SOURCE00/p1700000000000100"

// Browser (xoxc) auth forwards via the native chat.shareMessage method.
func TestForwardMessageBrowserUsesShareMessage(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("chat.shareMessage", map[string]any{"ok": true, "ts": "1.5", "channel": "C0DEST0001"})
	c := browserClient(t, server)

	res, err := ForwardMessage(context.Background(), c, "C0DEST0001",
		ForwardSource{ChannelID: "C0SOURCE00", TS: "1700000000.000100", Permalink: srcPermalink},
		OutgoingMessage{Text: "look at this"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TS != "1.5" {
		t.Errorf("ts = %q, want 1.5", res.TS)
	}
	if n := len(server.CallsFor("chat.postMessage")); n != 0 {
		t.Errorf("browser forward should not post; chat.postMessage called %d times", n)
	}
	call := server.CallsFor("chat.shareMessage")[0]
	// source coordinates and destination map to the right params.
	if call.Params.Get("channel") != "C0SOURCE00" || call.Params.Get("timestamp") != "1700000000.000100" {
		t.Errorf("source params = channel %q ts %q", call.Params.Get("channel"), call.Params.Get("timestamp"))
	}
	if call.Params.Get("share_channel") != "C0DEST0001" {
		t.Errorf("share_channel = %q, want the destination", call.Params.Get("share_channel"))
	}
	if call.Params.Get("text") != "look at this" {
		t.Errorf("caption text = %q", call.Params.Get("text"))
	}
}

// A caption-less forward sends an empty text and no blocks — matching the
// native client, which forwards with just the shared card.
func TestForwardMessageBrowserEmptyCaption(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("chat.shareMessage", map[string]any{"ok": true, "ts": "1.0", "channel": "C0DEST0001"})
	c := browserClient(t, server)

	if _, err := ForwardMessage(context.Background(), c, "C0DEST0001",
		ForwardSource{ChannelID: "C0SOURCE00", TS: "1700000000.000100", Permalink: srcPermalink},
		OutgoingMessage{}); err != nil {
		t.Fatal(err)
	}
	call := server.CallsFor("chat.shareMessage")[0]
	if call.Params.Get("text") != "" {
		t.Errorf("text = %q, want empty for a caption-less forward", call.Params.Get("text"))
	}
	if call.Params.Has("blocks") {
		t.Errorf("a caption-less forward should send no blocks; got %q", call.Params.Get("blocks"))
	}
}

// Non-browser tokens can't call chat.shareMessage, so they fall back to posting
// the permalink with unfurling forced on.
func TestForwardMessageStandardFallsBackToUnfurl(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "2.0", "channel": "C0DEST0001"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	c := New(Auth{Type: AuthStandard, Token: "xoxb-test"}, WithBaseURL(ts.URL))

	if _, err := ForwardMessage(context.Background(), c, "C0DEST0001",
		ForwardSource{ChannelID: "C0SOURCE00", TS: "1700000000.000100", Permalink: srcPermalink},
		OutgoingMessage{Text: "look at this"}); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("chat.shareMessage")); n != 0 {
		t.Errorf("standard token must not call chat.shareMessage, got %d", n)
	}
	call := server.CallsFor("chat.postMessage")[0]
	if got := call.Params.Get("text"); !strings.Contains(got, srcPermalink) || !strings.Contains(got, "look at this") {
		t.Errorf("text = %q, want comment + permalink", got)
	}
	if call.Params.Get("unfurl_links") != "true" {
		t.Errorf("unfurl_links = %q, want true", call.Params.Get("unfurl_links"))
	}
}
