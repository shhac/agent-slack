package slack

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// watchFixture wires a browser client to a server serving both the socket and
// the Web API, and returns the server so tests can add history fixtures.
func watchFixture(t *testing.T, script mockslack.WSScript) (*Client, *mockslack.Server) {
	t.Helper()
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	// The fake socket always stays open: a closed script would look like a
	// dropped connection and send the engine into its reconnect loop.
	script.KeepOpen = true
	server.EnableWebSocket(script)
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	return browserClientFor(ts.URL), server
}

func collectWatch(t *testing.T, c *Client, opts WatchOptions) ([]Event, WatchResult) {
	t.Helper()
	if opts.Duration == 0 {
		opts.Duration = 500 * time.Millisecond
	}
	var got []Event
	result, err := Watch(context.Background(), c, opts, func(e Event) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	return got, result
}

func TestWatchEmitsOnlyRealActivity(t *testing.T) {
	c, _ := watchFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	got, result := collectWatch(t, c, WatchOptions{Filter: EventFilter{IncludeThreadReplies: true}})
	if len(got) != 4 {
		t.Fatalf("emitted %d events, want the 4 messages in the script: %+v", len(got), got)
	}
	for _, event := range got {
		if event.Kind != EventMessage {
			t.Errorf("default filter should emit messages only, got %s", event.Kind)
		}
	}
	if result.Cursors[mockslack.WSChannelID] == "" {
		t.Error("a channel that produced events should have a cursor")
	}
}

func TestWatchThreadRepliesExcludedByDefault(t *testing.T) {
	c, _ := watchFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	got, _ := collectWatch(t, c, WatchOptions{Filter: EventFilter{Channels: []string{mockslack.WSChannelID}}})
	for _, event := range got {
		if event.ThreadTS != "" && event.ThreadTS != event.TS {
			t.Errorf("a channel watch should not emit thread replies: %+v", event)
		}
	}
}

func TestWatchStopsAtMaxEvents(t *testing.T) {
	c, _ := watchFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	got, result := collectWatch(t, c, WatchOptions{
		Filter:    EventFilter{IncludeThreadReplies: true},
		MaxEvents: 2,
	})
	if len(got) != 2 || result.StoppedBy != WatchStoppedMaxEvents {
		t.Fatalf("got %d events, stopped by %q", len(got), result.StoppedBy)
	}
}

func TestWatchReactionsAreOptIn(t *testing.T) {
	c, _ := watchFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	got, _ := collectWatch(t, c, WatchOptions{
		Filter: EventFilter{Kinds: []EventKind{EventReactionAdded, EventReactionRemoved}},
	})
	if len(got) != 3 {
		t.Fatalf("want the script's 3 reaction events, got %+v", got)
	}
}

// The backfill exists so a reply that landed between sending and waiting is
// still found. It must reach the caller through the same path as live frames.
func TestWatchBackfillsFromCursor(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000005.000100", mockslack.WSOtherUser, "before the cursor"),
		mockslack.Message("1700000015.000100", mockslack.WSOtherUser, "missed while we were away"),
	))

	got, result := collectWatch(t, c, WatchOptions{
		Filter:          EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		BackfillChannel: mockslack.WSChannelID,
		Duration:        30 * time.Second,
		MaxEvents:       1,
	})
	if len(got) != 1 {
		t.Fatalf("want the backfilled message, got %+v", got)
	}
	// The answer was already in the backfill: the run must end there, not sit
	// out the remaining timeout.
	if result.StoppedBy != WatchStoppedMaxEvents {
		t.Errorf("stopped_by = %q, want %q — a satisfied backfill must return at once",
			result.StoppedBy, WatchStoppedMaxEvents)
	}
	if got[0].Content != "missed while we were away" {
		t.Errorf("event = %+v; --since is exclusive, so the earlier message must not match", got[0])
	}
}

// A message delivered by both the backfill and the socket is one event.
func TestWatchDedupesBackfillAgainstLiveFrames(t *testing.T) {
	live := mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "the same message", "1700000015.000100")
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello(), live}})
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000015.000100", mockslack.WSOtherUser, "the same message"),
	))

	got, _ := collectWatch(t, c, WatchOptions{
		Filter:          EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		BackfillChannel: mockslack.WSChannelID,
		Duration:        400 * time.Millisecond,
	})
	if len(got) != 1 {
		t.Fatalf("the same message arrived twice and was emitted %d times: %+v", len(got), got)
	}
}

func TestWatchReportsSkippedInScopeEvents(t *testing.T) {
	c, _ := watchFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	var skipped []Event
	_, _ = collectWatch(t, c, WatchOptions{
		Filter: EventFilter{
			Kinds:     []EventKind{EventReactionAdded},
			Channels:  []string{mockslack.WSChannelID},
			Reactions: []string{"white_check_mark"},
		},
		Duration:  400 * time.Millisecond,
		OnSkipped: func(e Event) { skipped = append(skipped, e) },
	})
	if len(skipped) == 0 {
		t.Fatal("reactions that did not match must still be reported, or a 'no' looks like silence")
	}
	for _, event := range skipped {
		if event.ChannelID != mockslack.WSChannelID {
			t.Errorf("out-of-scope traffic should not be reported: %+v", event)
		}
	}
}

func TestAwaitReturnsFirstMatchWithCursor(t *testing.T) {
	c, _ := watchFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	result, err := Await(context.Background(), c, AwaitOptions{
		Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
		ChannelID: mockslack.WSChannelID,
		Timeout:   2 * time.Second,
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
		Filter:    EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		ChannelID: mockslack.WSChannelID,
		Timeout:   200 * time.Millisecond,
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
		ChannelID: mockslack.WSChannelID,
		Timeout:   400 * time.Millisecond,
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

func TestWatchPollFallbackFindsNewMessages(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000015.000100", mockslack.WSOtherUser, "posted while polling"),
	))

	got, _ := collectWatch(t, c, WatchOptions{
		Filter:          EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		BackfillChannel: mockslack.WSChannelID,
		Poll:            true,
		PollEvery:       10 * time.Millisecond,
		MaxEvents:       1,
	})
	if len(got) != 1 || got[0].Content != "posted while polling" {
		t.Fatalf("poll fallback emitted %+v", got)
	}
}

// A dropped socket must be invisible to the caller: reconnect, gap-fill, and
// do not re-emit what was already delivered.
func TestWatchReconnectsWithoutDuplicatingEvents(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	defer ts.Close()
	// KeepOpen false: the server hangs up once the script is exhausted, which
	// is exactly what a dropped connection looks like to the engine.
	server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{
		mockslack.Hello(),
		mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "only once", "1700000015.000100"),
	}})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	server.HandleBody("conversations.history", mockslack.History())
	c := browserClientFor(ts.URL)

	var reconnects int
	got, result := collectWatch(t, c, WatchOptions{
		Filter:          EventFilter{Channels: []string{mockslack.WSChannelID}},
		BackfillChannel: mockslack.WSChannelID,
		Duration:        400 * time.Millisecond,
		OnReconnect:     func(int) { reconnects++ },
	})
	if len(got) != 1 {
		t.Fatalf("emitted %d events across reconnects, want 1: %+v", len(got), got)
	}
	if reconnects == 0 || result.Reconnects == 0 {
		t.Fatal("a dropped socket should have been re-established")
	}
	// Gap-fill re-reads the conversation we know about, so no hole is recorded.
	if result.Gaps != 0 {
		t.Errorf("gaps = %d, want 0 when the channel is known and re-readable", result.Gaps)
	}
}
