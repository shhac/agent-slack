package cli

import (
	"strings"
	"testing"
)

func TestLaterList(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("saved.list", map[string]any{
		"ok": true,
		"counts": map[string]any{
			"uncompleted_count": float64(2), "archived_count": float64(1),
			"completed_count": float64(5), "total_count": float64(8),
		},
		"saved_items": []any{
			map[string]any{"item_id": "C12345678", "item_type": "message", "ts": "1770165109.000001", "state": "in_progress", "date_created": float64(1770000000)},
			map[string]any{"item_id": "F999", "item_type": "file", "state": "in_progress"},
		},
	})
	f.server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C12345678", "name": "general"},
	})
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.000001", "U1", "remember this"),
	))

	out, _, err := f.run(t, "later", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 { // 1 item (file filtered) + @counts
		t.Fatalf("lines = %v", lines)
	}
	item := lines[0]
	if item["channel_name"] != "general" || item["state"] != "in_progress" {
		t.Errorf("item = %v", item)
	}
	if item["message"].(map[string]any)["content"] != "remember this" {
		t.Errorf("message = %v", item["message"])
	}
	counts := lines[1]["@counts"].(map[string]any)
	if counts["total"] != float64(8) {
		t.Errorf("counts = %v", counts)
	}
}

func TestLaterListDialect(t *testing.T) {
	run := func(slackMarkdown bool) string {
		f := newCLIFixture(t)
		f.server.HandleBody("saved.list", map[string]any{"ok": true,
			"counts": map[string]any{"uncompleted_count": float64(1), "total_count": float64(1)},
			"saved_items": []any{map[string]any{"item_id": "C12345678", "item_type": "message",
				"ts": "1770165109.000001", "state": "in_progress"}}})
		f.server.HandleBody("conversations.info", map[string]any{"ok": true,
			"channel": map[string]any{"id": "C12345678", "name": "general"}})
		f.server.HandleBody("conversations.history", historyWith(
			simpleMessage("1770165109.000001", "U1", "a *bold* save")))
		args := []string{"later", "list"}
		if slackMarkdown {
			args = append(args, "--slack-markdown")
		}
		out, _, err := f.run(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		return parseNDJSON(t, out)[0]["message"].(map[string]any)["content"].(string)
	}
	if got := run(false); got != "a **bold** save" {
		t.Errorf("default = %q, want standard Markdown", got)
	}
	if got := run(true); got != "a *bold* save" {
		t.Errorf("--slack-markdown = %q, want native mrkdwn", got)
	}
}

func TestLaterCompleteUsesMultipartMark(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("saved.update", map[string]any{"ok": true})

	_, _, err := f.run(t, "later", "complete", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("saved.update")[0]
	if !strings.HasPrefix(call.Header.Get("Content-Type"), "multipart/form-data") {
		t.Errorf("content-type = %q (saved.update needs multipart)", call.Header.Get("Content-Type"))
	}
	if call.Params.Get("mark") != "completed" || call.Params.Get("item_id") != "C1A2B3C4D" {
		t.Errorf("params = %v", call.Params)
	}
}

func TestLaterRemind(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("saved.update", map[string]any{"ok": true})

	out, _, err := f.run(t, "later", "remind", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "--in", "3h")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["remind_at"] == nil {
		t.Errorf("out = %s", out)
	}
	if got := f.server.CallsFor("saved.update")[0].Params.Get("date_due"); got == "" {
		t.Error("date_due not sent")
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
