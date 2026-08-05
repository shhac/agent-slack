package slack

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestAwaitReturnsFirstMatchWithCursor(t *testing.T) {
	c, _ := watchFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	result, err := Await(context.Background(), c, AwaitOptions{
		Filter:  EventFilter{Channels: []string{mockslack.WSChannelID}},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Received || result.Event == nil {
		t.Fatalf("result = %+v", result)
	}
	if result.Cursor != result.Event.Cursor() {
		t.Errorf("cursor = %q, want the matched event's %q", result.Cursor, result.Event.Cursor())
	}
}

// A timeout is a successful outcome: no error, and a cursor that has not moved
// past anything unexamined.
func TestAwaitTimeoutEchoesCursor(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History())

	result, err := Await(context.Background(), c, AwaitOptions{
		Filter:  EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("a timeout is not an error: %v", err)
	}
	if result.Received || result.Event != nil {
		t.Fatalf("result = %+v", result)
	}
	if result.Cursor != "1700000010.000100" {
		t.Errorf("cursor = %q, want the input echoed back unchanged", result.Cursor)
	}
}

// The rejection case: awaiting a ✅ while someone reacts ❌.
func TestAwaitSurfacesTheRejectionItFilteredOut(t *testing.T) {
	rejection := mockslack.WSReactionAdded(mockslack.WSChannelID, mockslack.WSOtherUser,
		"x", "1700000010.000100", "1700000030.000100")
	c, _ := watchFixture(t, mockslack.WSScript{
		Frames:   []map[string]any{mockslack.Hello(), rejection},
		KeepOpen: true,
	})

	result, err := Await(context.Background(), c, AwaitOptions{
		Filter: EventFilter{
			Kinds:     []EventKind{EventReactionAdded},
			Channels:  []string{mockslack.WSChannelID},
			Reactions: []string{"white_check_mark"},
		},
		Timeout: 400 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Received {
		t.Fatal("the ❌ must not satisfy an await for ✅")
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Reaction != "x" {
		t.Fatalf("skipped = %+v; the rejection must be visible", result.Skipped)
	}
}

func TestAwaitPollFallbackNeedsAConversation(t *testing.T) {
	c, _ := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})

	_, err := Await(context.Background(), c, AwaitOptions{Poll: true, Timeout: time.Second})
	if err == nil {
		t.Fatal("polling a whole workspace is not viable and must be refused")
	}
}

// Past the skipped-report bound the cursor must freeze. Advancing over
// rejections the caller never saw loses them permanently on resume — the exact
// "a rejection must not look like silence" failure this feature exists to stop.
func TestAwaitCursorStopsAtTheSkippedReportBound(t *testing.T) {
	frames := []map[string]any{mockslack.Hello()}
	for i := range 6 {
		frames = append(frames, mockslack.WSReactionAdded(mockslack.WSChannelID, mockslack.WSOtherUser,
			"x", "1700000010.000100", fmt.Sprintf("17000000%02d.000100", 20+i)))
	}
	c, server := watchFixture(t, mockslack.WSScript{Frames: frames})
	server.HandleBody("conversations.history", mockslack.History())
	server.HandleBody("conversations.replies", mockslack.History())

	result, err := Await(context.Background(), c, AwaitOptions{
		Filter: EventFilter{
			Kinds:     []EventKind{EventReactionAdded},
			Channels:  []string{mockslack.WSChannelID},
			Reactions: []string{"white_check_mark"},
			Since:     "1700000010.000100",
		},
		Timeout:    400 * time.Millisecond,
		MaxSkipped: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skipped) != 2 || !result.SkippedTruncated {
		t.Fatalf("skipped=%d truncated=%v, want 2 and a truncation flag", len(result.Skipped), result.SkippedTruncated)
	}
	// Equality pins both halves at once: the cursor must advance OVER reported
	// rejections (an upper-bound-only assertion passes when it never advances
	// at all, falling back to --since) and must FREEZE at the last reported
	// one (resuming past an unreported rejection loses it for good).
	lastReported := result.Skipped[len(result.Skipped)-1].Cursor()
	if result.Cursor != lastReported {
		t.Errorf("cursor = %q, want exactly the last reported rejection %q", result.Cursor, lastReported)
	}
}

// Without --since a poll run has nothing to anchor to in an empty conversation.
// An empty cursor makes every read a no-op, so the await reports silence
// forever even as messages arrive.

// The resume cursor freezes at the skipped-report bound, but the engine's own
// read position must not — sharing one value made a truncated report stall the
// poll loop on a cursor it could never pass.
func TestWatchKeepsReadingPastATruncatedSkippedReport(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	rejections := make([]map[string]any, 0, 4)
	for i := range 4 {
		rejections = append(rejections, mockslack.Message(
			fmt.Sprintf("17000000%02d.000100", 20+i), mockslack.WSOtherUser, "not the answer"))
	}
	server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History(rejections...)},
		mockslack.Response{Body: mockslack.History(append(rejections,
			mockslack.Message("1700000099.000100", mockslack.WSBotID, "the answer"))...)},
	)

	// Only bot posts count; the human messages are in scope but excluded.
	result, err := Await(context.Background(), c, AwaitOptions{
		Filter: EventFilter{
			Since:    "1700000010.000100",
			Channels: []string{mockslack.WSChannelID},
			From:     []string{mockslack.WSBotID},
		},
		Poll:       true,
		PollEvery:  5 * time.Millisecond,
		Timeout:    2 * time.Second,
		MaxSkipped: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.SkippedTruncated {
		t.Fatalf("expected the skipped report to truncate: %+v", result)
	}
	if !result.Received {
		t.Fatal("polling must keep advancing past a truncated report and find the answer")
	}
}
