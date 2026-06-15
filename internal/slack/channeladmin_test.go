package slack

import (
	"context"
	"net/url"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// When every target is already a member, the result is a sensible no-op: an
// empty invited list and all ids under already_in_channel — not an error.
func TestInviteUsersAllAlreadyIn(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.invite", map[string]any{"ok": false, "error": "already_in_channel"})
	c := newStandardClient(t, server)

	res, err := InviteUsersToChannel(context.Background(), c, "C1", []string{"U1", "U2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.InvitedUserIDs) != 0 {
		t.Errorf("invited = %v, want none", res.InvitedUserIDs)
	}
	if len(res.AlreadyInChannelUserIDs) != 2 {
		t.Errorf("already-in = %v, want both", res.AlreadyInChannelUserIDs)
	}
}

// Nothing to invite → no API calls at all (the natural short-circuit).
func TestInviteUsersEmptyMakesNoCalls(t *testing.T) {
	server := mockslack.New()
	c := newStandardClient(t, server)

	res, err := InviteUsersToChannel(context.Background(), c, "C1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.InvitedUserIDs) != 0 || len(res.AlreadyInChannelUserIDs) != 0 {
		t.Errorf("res = %+v, want empty", res)
	}
	if n := len(server.CallsFor("conversations.invite")); n != 0 {
		t.Errorf("expected no API calls for an empty invite, got %d", n)
	}
}

func TestInviteUsersMixed(t *testing.T) {
	server := mockslack.New()
	server.HandleWhen("conversations.invite", func(p url.Values) bool { return p.Get("users") == "U1" },
		mockslack.Response{Body: map[string]any{"ok": true}})
	server.HandleWhen("conversations.invite", func(p url.Values) bool { return p.Get("users") == "U2" },
		mockslack.Response{Body: map[string]any{"ok": false, "error": "already_in_channel"}})
	c := newStandardClient(t, server)

	res, err := InviteUsersToChannel(context.Background(), c, "C1", []string{"U1", "U2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.InvitedUserIDs) != 1 || res.InvitedUserIDs[0] != "U1" {
		t.Errorf("invited = %v, want [U1]", res.InvitedUserIDs)
	}
	if len(res.AlreadyInChannelUserIDs) != 1 || res.AlreadyInChannelUserIDs[0] != "U2" {
		t.Errorf("already-in = %v, want [U2]", res.AlreadyInChannelUserIDs)
	}
}

// A non-already-in error aborts the batch rather than being swallowed.
func TestInviteUsersHardErrorAborts(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.invite", map[string]any{"ok": false, "error": "channel_not_found"})
	c := newStandardClient(t, server)

	if _, err := InviteUsersToChannel(context.Background(), c, "C1", []string{"U1"}); err == nil {
		t.Fatal("a non-already-in error should propagate")
	}
}
