package cli

import (
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestMessageGetByPermalink(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.628379", "U12345678", "Hello <@U87654321> :rocket:"),
	))

	out, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C0123ABCD/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	msg := payload["message"].(map[string]any)
	if msg["content"] != "Hello @U87654321 🚀" {
		t.Errorf("content = %q", msg["content"])
	}
	if msg["ts"] != "1770165109.628379" || msg["channel_id"] != "C0123ABCD" {
		t.Errorf("msg = %v", msg)
	}
	if payload["permalink"] != "https://acme.slack.com/archives/C0123ABCD/p1770165109628379" {
		t.Errorf("permalink = %v", payload["permalink"])
	}
	if _, hasThread := payload["thread"]; hasThread {
		t.Error("no thread expected for a plain message")
	}
}

func TestMessageGetDialect(t *testing.T) {
	link := "https://acme.slack.com/archives/C0123ABCD/p1770165109628379"

	// Default: Slack mrkdwn is converted to standard Markdown.
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.628379", "U12345678", "this is *bold* and ~gone~")))
	out, _, err := f.run(t, "message", "get", link)
	if err != nil {
		t.Fatal(err)
	}
	if got := parseJSON(t, out)["message"].(map[string]any)["content"]; got != "this is **bold** and ~~gone~~" {
		t.Errorf("default content = %q, want standard Markdown", got)
	}

	// --slack-markdown opts out: native Slack mrkdwn is preserved.
	f2 := newCLIFixture(t)
	f2.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.628379", "U12345678", "this is *bold* and ~gone~")))
	out2, _, err := f2.run(t, "message", "get", link, "--slack-markdown")
	if err != nil {
		t.Fatal(err)
	}
	if got := parseJSON(t, out2)["message"].(map[string]any)["content"]; got != "this is *bold* and ~gone~" {
		t.Errorf("--slack-markdown content = %q, want native Slack mrkdwn", got)
	}
}

func TestMessageGetThreadSummary(t *testing.T) {
	f := newCLIFixture(t)
	msg := simpleMessage("1770165109.628379", "U12345678", "root")
	msg["reply_count"] = 4
	f.server.HandleBody("conversations.history", historyWith(msg))

	out, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	thread := parseJSON(t, out)["thread"].(map[string]any)
	if thread["ts"] != "1770165109.628379" || thread["length"] != float64(5) {
		t.Errorf("thread = %v", thread)
	}
}

func TestMessageGetChannelRequiresTS(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "get", "#general")
	if err == nil {
		t.Fatal("expected error")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestMessageListChannelHistory(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165110.000002", "U2", "second"),
		simpleMessage("1770165109.000001", "U1", "first"),
	))

	out, _, err := f.run(t, "message", "list", "#general")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 3 { // 2 messages + @channel_id meta
		t.Fatalf("lines = %d: %s", len(lines), out)
	}
	// Chronological order.
	if lines[0]["content"] != "first" || lines[1]["content"] != "second" {
		t.Errorf("order wrong: %v", lines)
	}
	if lines[2]["@channel_id"] != "C123" {
		t.Errorf("meta = %v", lines[2])
	}
}

func TestMessageListThread(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	root := simpleMessage("1.000001", "U1", "root")
	root["thread_ts"] = "1.000001"
	root["reply_count"] = 1
	reply := simpleMessage("2.000002", "U2", "reply")
	reply["thread_ts"] = "1.000001"
	f.server.HandleBody("conversations.replies", map[string]any{
		"ok":       true,
		"messages": []any{root, reply},
	})

	out, _, err := f.run(t, "message", "list", "#general", "--thread-ts", "1.000001")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 4 { // 2 messages + @channel_id + @thread_ts
		t.Fatalf("lines = %d: %s", len(lines), out)
	}
	// channel_id/thread_ts stripped from rows (they're in the meta lines).
	if _, has := lines[0]["channel_id"]; has {
		t.Error("thread rows should not repeat channel_id")
	}
}

func TestMessageListReactionFiltersRequireOldest(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C123")
	_, stderr, err := f.run(t, "message", "list", "#general", "--with-reaction", "eyes")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(errPayload(t, stderr)["error"].(string), "--oldest") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestMessageGetDownloadsFiles(t *testing.T) {
	f := newCLIFixture(t)
	host := fileHost(t, "image/png", "IMGDATA")
	msg := simpleMessage("1770165109.628379", "U1", "see attachment")
	msg["files"] = []any{map[string]any{
		"id": "F77777777", "name": "shot.png", "mimetype": "image/png",
		"url_private_download": host.URL + "/shot",
	}}
	f.server.HandleBody("conversations.history", historyWith(msg))

	out, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	files := parseJSON(t, out)["message"].(map[string]any)["files"].([]any)
	file := files[0].(map[string]any)
	if !strings.HasSuffix(file["path"].(string), "F77777777.png") {
		t.Errorf("file = %v", file)
	}

	// --no-download keeps it metadata-free (no downloadedPaths entry → no files key).
	out2, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "--no-download")
	if err != nil {
		t.Fatal(err)
	}
	if _, has := parseJSON(t, out2)["message"].(map[string]any)["files"]; has {
		t.Error("--no-download should omit files (no local paths to report)")
	}
}

// --resolve cached expands referenced channel + usergroup ids (which arrive as
// bare ids in mentions) into referenced_channels / referenced_usergroups, the
// channel/usergroup analogs of referenced_users.
func TestMessageGetResolvesChannelsAndUsergroups(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.628379", "U12345678",
			"see <#C0ABCDEF1|general> ping <!subteam^S0TEAM1234>")))
	f.server.HandleBody("conversations.info", mockslack.ChannelInfo("C0ABCDEF1", "general-updates"))
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0TEAM1234", "productteam", "Product Team")))

	out, _, err := f.run(t, "message", "get",
		"https://acme.slack.com/archives/C0123ABCD/p1770165109628379", "--resolve", "auto")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	chans, _ := payload["referenced_channels"].(map[string]any)
	if c, _ := chans["C0ABCDEF1"].(map[string]any); c == nil || c["name"] != "general-updates" {
		t.Errorf("referenced_channels = %v", payload["referenced_channels"])
	}
	groups, _ := payload["referenced_usergroups"].(map[string]any)
	if g, _ := groups["S0TEAM1234"].(map[string]any); g == nil || g["handle"] != "productteam" {
		t.Errorf("referenced_usergroups = %v", payload["referenced_usergroups"])
	}
}
