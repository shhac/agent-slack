package slack

import (
	"context"
	"fmt"
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

	// The timeout is far longer than the work, so a run that fails to notice
	// its cap was met in the backfill blocks for the whole of it. Timing is
	// the property under test: asserting only stopped_by would still pass a
	// build that returned the right answer minutes late.
	const timeout = 3 * time.Second
	started := time.Now()
	got, result := collectWatch(t, c, WatchOptions{
		Filter:    EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration:  timeout,
		MaxEvents: 1,
	})
	elapsed := time.Since(started)
	if len(got) != 1 {
		t.Fatalf("want the backfilled message, got %+v", got)
	}
	if elapsed > timeout/3 {
		t.Errorf("took %s of a %s timeout; a satisfied backfill must return at once, not wait it out",
			elapsed, timeout)
	}
	if result.StoppedBy != WatchStoppedMaxEvents {
		t.Errorf("stopped_by = %q, want %q", result.StoppedBy, WatchStoppedMaxEvents)
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
		Filter:   EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration: 400 * time.Millisecond,
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

func TestWatchPollFallbackFindsNewMessages(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000015.000100", mockslack.WSOtherUser, "posted while polling"),
	))

	got, _ := collectWatch(t, c, WatchOptions{
		Filter:    EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Poll:      true,
		PollEvery: 10 * time.Millisecond,
		MaxEvents: 1,
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
	// The first socket hangs up once its script is exhausted — exactly what a
	// dropped connection looks like to the engine — and the replacement stays
	// up, so the run reconnects exactly once.
	server.EnableWebSocket(mockslack.WSScript{
		Frames: []map[string]any{
			mockslack.Hello(),
			mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "only once", "1700000015.000100"),
		},
		HangUpAfterScript: 1,
	})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	server.HandleBody("conversations.history", mockslack.History())
	c := browserClientFor(ts.URL)

	var reconnects int
	got, result := collectWatch(t, c, WatchOptions{
		// A --since floor makes the gap-fill deterministic: without one, a drop
		// that lands before any event is examined has nothing to re-read from
		// and correctly records a gap, which would make this test race.
		Filter:      EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration:    400 * time.Millisecond,
		OnReconnect: func(int) { reconnects++ },
	})
	if len(got) != 1 {
		t.Fatalf("emitted %d events across reconnects, want 1: %+v", len(got), got)
	}
	if reconnects != 1 || result.Reconnects != 1 {
		t.Fatalf("reconnects = %d (callback %d), want exactly 1", result.Reconnects, reconnects)
	}
	// Gap-fill re-reads the conversation we know about, so no hole is recorded.
	if result.Gaps != 0 {
		t.Errorf("gaps = %d, want 0 when the channel is known and re-readable", result.Gaps)
	}
}

// A reply threaded on the awaited message does not appear in channel history,
// so the backfill has to read the thread too or it misses answers that landed
// before the await started.
func TestWatchBackfillReadsRepliesToTheAwaitedMessage(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History()) // nothing at channel level
	server.HandleBody("conversations.replies", mockslack.History(
		mockslack.Message("1700000010.000100", mockslack.WSUserID, "the question"),
		mockslack.Message("1700000020.000200", mockslack.WSOtherUser, "answered in the thread"),
	))

	got, _ := collectWatch(t, c, WatchOptions{
		Filter: EventFilter{
			Since:     "1700000010.000100",
			Channels:  []string{mockslack.WSChannelID},
			RepliesTo: "1700000010.000100",
		},
		MaxEvents: 1,
	})
	if len(got) != 1 || got[0].Content != "answered in the thread" {
		t.Fatalf("backfill missed the in-thread answer: %+v", got)
	}
}

// --since may be a cursor from an earlier run rather than a message that
// started a thread. The speculative thread read must not fail the await.
func TestWatchTolerantOfAnUnreadableRepliesBackfill(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{
		mockslack.Hello(),
		mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "live answer", "1700000030.000300"),
	}})
	server.HandleBody("conversations.history", mockslack.History())
	// No conversations.replies fixture: the call errors, as it would for a ts
	// that never started a thread.

	got, _ := collectWatch(t, c, WatchOptions{
		Filter: EventFilter{
			Since:     "1700000010.000100",
			Channels:  []string{mockslack.WSChannelID},
			RepliesTo: "1700000010.000100",
		},
		MaxEvents: 1,
	})
	if len(got) != 1 || got[0].Content != "live answer" {
		t.Fatalf("a failed thread read must not sink the await: %+v", got)
	}
}

// Without --since a poll run has no cursor, and "everything in history" is the
// wrong answer — the caller asked what happens next. The conversation's
// current tip becomes the baseline, so only later messages are emitted.
func TestWatchPollBaselineStartsAtTheConversationTip(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	// First read establishes the tip; later reads include a newer message.
	server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000010.000100", mockslack.WSOtherUser, "already there"),
		)},
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000010.000100", mockslack.WSOtherUser, "already there"),
			mockslack.Message("1700000020.000200", mockslack.WSOtherUser, "arrived after we started"),
		)},
	)

	got, _ := collectWatch(t, c, WatchOptions{
		Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
		Poll:      true,
		PollEvery: 10 * time.Millisecond,
		MaxEvents: 1,
	})
	if len(got) != 1 || got[0].Content != "arrived after we started" {
		t.Fatalf("poll should start at the tip, not replay history: %+v", got)
	}
}

// A dead socket is not a cancellation. An agent that cannot tell the two apart
// treats a lost stream as a deliberate stop and never resumes.
func TestWatchReportsReconnectFailureDistinctlyFromCancellation(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	// The socket hangs up after hello, and the whole server goes away, so every
	// redial fails — the reconnect-exhausted path.
	server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	server.HandleBody("conversations.history", mockslack.History())
	c := browserClientFor(ts.URL)

	_, result := collectWatch(t, c, WatchOptions{
		Filter:      EventFilter{Channels: []string{mockslack.WSChannelID}},
		Duration:    3 * time.Second,
		OnReconnect: func(int) { ts.Close() }, // the gateway goes away mid-run
	})
	if result.StoppedBy != WatchStoppedReconnectFailed {
		t.Errorf("stopped_by = %q, want %q — a lost socket must not read as a cancellation",
			result.StoppedBy, WatchStoppedReconnectFailed)
	}
}

// The deadline cases still report themselves correctly.
func TestWatchReportsDurationAndCancellation(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History())

	_, result := collectWatch(t, c, WatchOptions{
		Filter:   EventFilter{Channels: []string{mockslack.WSChannelID}},
		Duration: 150 * time.Millisecond,
	})
	if result.StoppedBy != StoppedByDuration {
		t.Errorf("stopped_by = %q, want %q", result.StoppedBy, StoppedByDuration)
	}

	// The caller giving up mid-run is a cancellation, not a duration expiry:
	// the run has no duration of its own to expire.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	cancelled, err := Watch(ctx, c, WatchOptions{Filter: EventFilter{Channels: []string{mockslack.WSChannelID}}},
		func(Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.StoppedBy != StoppedByCancel {
		t.Errorf("stopped_by = %q, want %q", cancelled.StoppedBy, StoppedByCancel)
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
	lastReported := result.Skipped[len(result.Skipped)-1].Cursor()
	if tsAfter(result.Cursor, lastReported) {
		t.Errorf("cursor %q advanced past the last reported rejection %q; resuming there loses the ones never shown",
			result.Cursor, lastReported)
	}
}

// Without --since a poll run has nothing to anchor to in an empty conversation.
// An empty cursor makes every read a no-op, so the await reports silence
// forever even as messages arrive.
func TestWatchPollBaselineOnAnEmptyConversation(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History()}, // empty: no tip to start from
		// Arriving after the run started, which is the only thing an empty
		// conversation's baseline can mean.
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("9999999999.000100", mockslack.WSOtherUser, "the first message ever"),
		)},
	)

	got, _ := collectWatch(t, c, WatchOptions{
		Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
		Poll:      true,
		PollEvery: 10 * time.Millisecond,
		MaxEvents: 1,
		Duration:  2 * time.Second,
	})
	if len(got) != 1 || got[0].Content != "the first message ever" {
		t.Fatalf("a poll on an empty conversation must still deliver: %+v", got)
	}
}

// --idle-timeout is documented as "no matching event". On a busy workspace the
// firehose would reset it forever if any classified frame counted.
func TestWatchIdleTimeoutIgnoresNonMatchingTraffic(t *testing.T) {
	// Reactions keep arriving; the filter wants messages. Idle must still trip.
	frames := []map[string]any{mockslack.Hello()}
	for i := range 8 {
		frames = append(frames, mockslack.WSReactionAdded(mockslack.WSChannelID, mockslack.WSOtherUser,
			"eyes", "1700000010.000100", fmt.Sprintf("17000000%02d.000100", 30+i)))
	}
	c, server := watchFixture(t, mockslack.WSScript{Frames: frames, Interval: 10 * time.Millisecond})
	server.HandleBody("conversations.history", mockslack.History())

	_, result := collectWatch(t, c, WatchOptions{
		Filter:      EventFilter{Channels: []string{mockslack.WSChannelID}},
		IdleTimeout: 60 * time.Millisecond,
		Duration:    3 * time.Second,
	})
	if result.StoppedBy != WatchStoppedIdle {
		t.Errorf("stopped_by = %q, want %q — non-matching traffic must not hold the timer open",
			result.StoppedBy, WatchStoppedIdle)
	}
}

// A catch-up from a stale cursor must follow pages. Stopping at the first
// silently drops older post-cursor messages while reporting no gap.
func TestWatchBackfillFollowsHistoryPages(t *testing.T) {
	firstPage := make([]map[string]any, 0, backfillPageLimit)
	for i := range backfillPageLimit {
		firstPage = append(firstPage, mockslack.Message(
			fmt.Sprintf("17000001%02d.000100", i), mockslack.WSOtherUser, "page one"))
	}
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History(firstPage...)},
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000099.000100", mockslack.WSOtherUser, "on the second page"),
		)},
	)

	var got []Event
	_, err := Watch(context.Background(), c, WatchOptions{
		Filter:   EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration: 2 * time.Second,
	}, func(e Event) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) <= backfillPageLimit {
		t.Fatalf("emitted %d events; a second page was never read", len(got))
	}
}

// Reconnecting before anything matched still has --since as a floor to re-read
// from. Without it the catch-up reads nothing and records no gap, so the caller
// is told the stream is intact when it is not.
func TestWatchGapFillSeedsFromSince(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	defer ts.Close()
	server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000015.000100", mockslack.WSOtherUser, "arrived during the gap"),
	))
	c := browserClientFor(ts.URL)

	got, result := collectWatch(t, c, WatchOptions{
		Filter:   EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration: 600 * time.Millisecond,
	})
	if len(got) == 0 || got[0].Content != "arrived during the gap" {
		t.Fatalf("the gap-fill found nothing: %+v", got)
	}
	if result.Gaps != 0 {
		t.Errorf("gaps = %d, want 0 when the channel is re-readable", result.Gaps)
	}
}

// A workspace-wide stream has no channel list to re-read, so a reconnect
// leaves a hole. Gaps is how a caller learns its stream is not complete.
func TestWatchRecordsGapWhenItCannotRefill(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	defer ts.Close()
	server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	c := browserClientFor(ts.URL)

	_, result := collectWatch(t, c, WatchOptions{
		Filter:   EventFilter{}, // no channels: the default `message stream`
		Duration: 600 * time.Millisecond,
	})
	if result.Reconnects == 0 {
		t.Fatal("expected the dropped socket to be re-established")
	}
	if result.Gaps == 0 {
		t.Error("a reconnect with nothing to re-read must be reported as a gap")
	}
}

// The run's target comes from the filter, so backfill, gap-fill, and polling
// can never address a different conversation than the one being filtered for.
func TestWatchTargetComesFromTheFilter(t *testing.T) {
	one := WatchOptions{Filter: EventFilter{Channels: []string{"C0FAKEONE1"}}}
	if got := one.targetChannel(); got != "C0FAKEONE1" {
		t.Errorf("targetChannel = %q", got)
	}
	several := WatchOptions{Filter: EventFilter{Channels: []string{"C0FAKEONE1", "C0FAKETWO2"}}}
	if got := several.targetChannel(); got != "" {
		t.Errorf("a multi-channel run has no single target, got %q", got)
	}
	if got := (WatchOptions{}).targetChannel(); got != "" {
		t.Errorf("a workspace-wide run has no target, got %q", got)
	}
}
