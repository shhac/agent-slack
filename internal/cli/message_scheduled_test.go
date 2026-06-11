package cli

import (
	"testing"
)

func TestScheduledCancelRequiresYes(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "scheduled", "cancel", "Q123", "--channel", "C1A2B3C4D")
	if err == nil {
		t.Fatal("expected error")
	}
	if errPayload(t, stderr)["fixable_by"] != "human" {
		t.Errorf("stderr = %s", stderr)
	}

	f.server.HandleBody("chat.deleteScheduledMessage", map[string]any{"ok": true})
	out, _, err := f.run(t, "message", "scheduled", "cancel", "Q123", "--channel", "C1A2B3C4D", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["scheduled_message_id"] != "Q123" {
		t.Errorf("out = %s", out)
	}
}

func TestScheduledList(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("chat.scheduledMessages.list", map[string]any{
		"ok":                 true,
		"scheduled_messages": []any{map[string]any{"id": "Q1", "post_at": float64(2000000000)}},
	})
	out, _, err := f.run(t, "message", "scheduled", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 || lines[0]["id"] != "Q1" {
		t.Errorf("lines = %v", lines)
	}
}
