package cli

import (
	"strings"
	"testing"
)

func TestUnreadsTranscript(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("client.counts", map[string]any{
		"ok": true,
		"channels": []any{map[string]any{
			"id": "C12345678", "has_unreads": true, "unread_count_display": float64(2),
			"mention_count": float64(1), "last_read": "1770165000.000000",
		}},
		"ims": []any{}, "threads": map[string]any{},
	})
	f.server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C12345678", "name": "general"},
	})
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.000001", "U1", "unread one"),
	))

	out, _, err := f.run(t, "unreads", "--format", "transcript", "--tz", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"──── Unreads · 1 channel · 2 unread · 1 mention ────",
		"#general · 2 unread, 1 mention",
		"<U1|U1>",
		"unread one",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q\n%s", want, out)
		}
	}
}

func TestLaterListTranscript(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("saved.list", map[string]any{
		"ok": true,
		"counts": map[string]any{
			"uncompleted_count": float64(1), "archived_count": float64(0),
			"completed_count": float64(0), "total_count": float64(1),
		},
		"saved_items": []any{
			map[string]any{"item_id": "C12345678", "item_type": "message", "ts": "1770165109.000001", "state": "in_progress", "date_created": float64(1770000000)},
		},
	})
	f.server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C12345678", "name": "general"},
	})
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.000001", "U1", "remember this"),
	))

	out, _, err := f.run(t, "later", "list", "--format", "transcript", "--tz", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"──── Later · 1 saved · 1 in progress ────",
		"In progress",
		"#general · saved ",
		"<U1|U1>",
		"remember this",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q\n%s", want, out)
		}
	}
}

func TestDraftListTranscript(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C12345678", "name": "general"},
	})
	f.server.HandleBody("drafts.list", map[string]any{"ok": true, "drafts": []any{
		draftObj("Dr1", "C12345678", "deploy summary", 0),
	}})

	out, _, err := f.run(t, "message", "draft", "list", "--format", "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"──── Drafts · 1 draft ────", "Dr1 → #general", "deploy summary"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q\n%s", want, out)
		}
	}
}
