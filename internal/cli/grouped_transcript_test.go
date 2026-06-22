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

func TestChannelListTranscript(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.conversations", map[string]any{
		"ok": true,
		"channels": []any{
			map[string]any{"id": "C1", "name": "general", "is_member": true, "num_members": float64(42),
				"topic": map[string]any{"value": "All things acme"}},
			map[string]any{"id": "C2", "name": "secret", "is_private": true, "num_members": float64(3)},
		},
		"response_metadata": map[string]any{"next_cursor": "cur2"},
	})

	out, _, err := f.run(t, "channel", "list", "--format", "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"──── Channels · 2 channels · more available ────",
		"#general · 42 members · ✓ member",
		"All things acme",
		"#secret · 3 members · 🔒 private",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q\n%s", want, out)
		}
	}
}

func TestUserListTranscript(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{
		map[string]any{"id": "U1", "name": "alice", "real_name": "Alice A", "profile": map[string]any{"title": "Staff Eng"}},
	}})
	f.server.HandleBody("conversations.list", map[string]any{"ok": true, "channels": []any{}})

	out, _, err := f.run(t, "user", "list", "--format", "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"──── Users · 1 user ────", "@alice · Alice A · Staff Eng"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q\n%s", want, out)
		}
	}
}

func TestUsergroupGetTranscriptUnresolved(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{
		map[string]any{"id": "S1", "handle": "eng", "name": "Engineering", "user_count": float64(12),
			"description": "All engineers"},
	}})

	out, _, err := f.run(t, "usergroup", "get", "@eng", "@ghosts", "--format", "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"──── Usergroups · 1 usergroup ────",
		"@eng (Engineering) · 12 members",
		"All engineers",
		"Unresolved",
		"@ghosts — not found",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q\n%s", want, out)
		}
	}
}

func TestUsergroupGetTranscriptAllMissed(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", map[string]any{"ok": true, "usergroups": []any{}})

	out, _, err := f.run(t, "usergroup", "get", "@ghost1", "@ghost2", "--format", "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"@ghost1 — not found", "@ghost2 — not found"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q\n%s", want, out)
		}
	}
	// 0 resolved → an Unresolved section directly under the summary, with no
	// stray empty leading block.
	if !strings.Contains(out, "0 usergroups ────\n\nUnresolved") {
		t.Errorf("all-missed digest should have no empty leading block:\n%s", out)
	}
}

func TestCanvasGetTranscript(t *testing.T) {
	f := newCLIFixture(t)
	host := fileHost(t, "text/html", `<html><body><main><h1>Plan</h1><p>Step <strong>one</strong></p></main></body></html>`)
	f.server.HandleBody("files.info", map[string]any{
		"ok": true, "file": map[string]any{"id": "F08012345AB", "title": "Q3 Plan",
			"url_private_download": host.URL + "/canvas"},
	})

	out, _, err := f.run(t, "canvas", "get", "F08012345AB", "--format", "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"──── Q3 Plan ────", "# Plan", "**one**"} {
		if !strings.Contains(out, want) {
			t.Errorf("transcript missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, `"markdown"`) {
		t.Errorf("transcript should not be JSON-wrapped:\n%s", out)
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
