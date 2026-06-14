package cli

import (
	"testing"
)

func TestUnreadsDialect(t *testing.T) {
	run := func(slackMarkdown bool) string {
		f := newCLIFixture(t)
		f.server.HandleBody("client.counts", map[string]any{"ok": true,
			"channels": []any{map[string]any{"id": "C12345678", "has_unreads": true,
				"unread_count_display": float64(1), "last_read": "1770165000.000000"}},
			"ims": []any{}, "threads": map[string]any{}})
		f.server.HandleBody("conversations.info", map[string]any{"ok": true,
			"channel": map[string]any{"id": "C12345678", "name": "general"}})
		f.server.HandleBody("conversations.history", historyWith(
			simpleMessage("1770165109.000001", "U1", "a *bold* note")))
		args := []string{"unreads"}
		if slackMarkdown {
			args = append(args, "--slack-markdown")
		}
		out, _, err := f.run(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		msgs := parseNDJSON(t, out)[0]["messages"].([]any)
		return msgs[0].(map[string]any)["content"].(string)
	}
	if got := run(false); got != "a **bold** note" {
		t.Errorf("default = %q, want standard Markdown", got)
	}
	if got := run(true); got != "a *bold* note" {
		t.Errorf("--slack-markdown = %q, want native mrkdwn", got)
	}
}

func TestUnreads(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("client.counts", map[string]any{
		"ok": true,
		"channels": []any{map[string]any{
			"id": "C12345678", "has_unreads": true, "unread_count_display": float64(2),
			"mention_count": float64(1), "last_read": "1770165000.000000",
		}},
		"ims":     []any{},
		"threads": map[string]any{"has_unreads": true, "mention_count": float64(3)},
	})
	f.server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C12345678", "name": "general"},
	})
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.000001", "U1", "unread one"),
		map[string]any{"ts": "1770165110.000002", "user": "U2", "text": "joined", "subtype": "channel_join"},
	))

	out, _, err := f.run(t, "unreads")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 { // channel + @threads
		t.Fatalf("lines = %v", lines)
	}
	ch := lines[0]
	if ch["channel_name"] != "general" || ch["unread_count"] != float64(2) {
		t.Errorf("channel = %v", ch)
	}
	messages := ch["messages"].([]any)
	if len(messages) != 1 { // system join filtered
		t.Errorf("messages = %v", messages)
	}
	threads := lines[1]["@threads"].(map[string]any)
	if threads["mention_count"] != float64(3) {
		t.Errorf("threads = %v", threads)
	}
}
