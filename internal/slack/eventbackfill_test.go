package slack

import (
	"slices"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
	"github.com/shhac/agent-slack/internal/render"
)

func TestCatchUpEventsOrdersMessagesAndReactionsByCursor(t *testing.T) {
	const since = "1700000030.000100"
	summary := func(raw map[string]any) render.MessageSummary { return SummaryFromRaw("C1", raw) }
	messages := []render.MessageSummary{
		summary(mockslack.Message("1700000040.000100", mockslack.WSOtherUser, "later")),
		summary(mockslack.WithReactions(mockslack.Message("1700000010.000100", mockslack.WSUserID, "older"), "eyes", mockslack.WSOtherUser)),
		summary(mockslack.WithReactions(mockslack.Message(since, mockslack.WSUserID, "posted"), "+1", mockslack.WSOtherUser, "U0FAKEROBIN")),
	}

	var got []string
	for _, e := range catchUpEvents("C1", messages, since) {
		got = append(got, string(e.Kind)+"@"+e.Cursor()+":"+e.AuthorID())
	}
	want := []string{
		// Reactions on the --since message, just after it; the message itself
		// is not replayed.
		"reaction_added@1700000030.000101:" + mockslack.WSOtherUser,
		"reaction_added@1700000030.000101:U0FAKEROBIN",
		"message@1700000040.000100:" + mockslack.WSOtherUser,
		// The 👀 on the older message cannot be dated, so it is not caught up.
	}
	if !slices.Equal(got, want) {
		t.Errorf("catchUpEvents =\n  %v\nwant\n  %v", got, want)
	}
}
