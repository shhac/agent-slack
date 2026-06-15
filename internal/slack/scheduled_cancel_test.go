package slack

import (
	"context"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// Standard (bot) tokens cancel via chat.deleteScheduledMessage, which needs the
// channel.
func TestCancelScheduledStandard(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("chat.deleteScheduledMessage", map[string]any{"ok": true})
	c := newStandardClient(t, server)

	if err := CancelScheduledMessage(context.Background(), c, "C1", "Q123"); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("drafts.delete")); n != 0 {
		t.Errorf("standard auth must not delete a draft, got %d calls", n)
	}
	call := server.CallsFor("chat.deleteScheduledMessage")[0]
	if call.Params.Get("channel") != "C1" || call.Params.Get("scheduled_message_id") != "Q123" {
		t.Errorf("params = %v", call.Params)
	}
}

// (The browser draft-delete path is covered by
// TestCancelScheduledMessageBrowserDeletesDraft in drafts_test.go.)
