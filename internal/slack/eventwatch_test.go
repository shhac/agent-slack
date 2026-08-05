package slack

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// socketFixture wires a browser client to a server serving both the socket and
// the Web API, and returns the server so tests can add fixtures. The script is
// used as given: a helper that forces KeepOpen silently voids a caller's
// HangUpAfterScript, which is what drove every drop test to hand-roll its own
// server.
func socketFixture(t *testing.T, script mockslack.WSScript) (*Client, *mockslack.Server) {
	t.Helper()
	c, server, _ := socketFixtureAt(t, script)
	return c, server
}

// socketFixtureAt also hands back the server's base URL, for the few tests
// that need to build their own client.getWebSocketURL responses.
func socketFixtureAt(t *testing.T, script mockslack.WSScript) (*Client, *mockslack.Server, string) {
	t.Helper()
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	server.EnableWebSocket(script)
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	return browserClientFor(ts.URL), server, ts.URL
}

// watchFixture is socketFixture with a socket that stays up, which is what
// every test that is not about reconnection wants.
func watchFixture(t *testing.T, script mockslack.WSScript) (*Client, *mockslack.Server) {
	t.Helper()
	script.KeepOpen = true
	return socketFixture(t, script)
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
	// Without a positive count this passes when delivery is broken entirely.
	if len(got) == 0 {
		t.Fatal("the script's channel messages should have been delivered")
	}
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
	if got[0].Content() != "missed while we were away" {
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
	if len(got) != 1 || got[0].Content() != "posted while polling" {
		t.Fatalf("poll fallback emitted %+v", got)
	}
}

// A dropped socket must be invisible to the caller: reconnect, gap-fill, and
// do not re-emit what was already delivered.
func TestWatchReconnectsWithoutDuplicatingEvents(t *testing.T) {
	// The first socket hangs up once its script is exhausted — exactly what a
	// dropped connection looks like to the engine — and the replacement stays
	// up, so the run reconnects exactly once.
	c, server := socketFixture(t, mockslack.WSScript{
		Frames: []map[string]any{
			mockslack.Hello(),
			mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "only once", "1700000015.000100"),
		},
		HangUpAfterScript: 1,
	})
	server.HandleBody("conversations.history", mockslack.History())

	var reconnects int
	got, result := collectWatch(t, c, WatchOptions{
		// A --since floor makes the gap-fill deterministic: without one, a drop
		// that lands before any event is examined has nothing to re-read from
		// and correctly records a gap, which would make this test race.
		Filter:      EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration:    400 * time.Millisecond,
		OnReconnect: func(int, bool) { reconnects++ },
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
	if len(got) != 1 || got[0].Content() != "answered in the thread" {
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
	if len(got) != 1 || got[0].Content() != "live answer" {
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
	if len(got) != 1 || got[0].Content() != "arrived after we started" {
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
		OnReconnect: func(int, bool) { ts.Close() }, // the gateway goes away mid-run
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
	if len(got) != 1 || got[0].Content() != "the first message ever" {
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
	// One bounded drop, so the gap-fill runs exactly once rather than the run
	// flapping as fast as the retry loop allows.
	c, server := socketFixture(t, mockslack.WSScript{
		Frames:            []map[string]any{mockslack.Hello()},
		HangUpAfterScript: 1,
	})
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000015.000100", mockslack.WSOtherUser, "arrived during the gap"),
	))

	got, result := collectWatch(t, c, WatchOptions{
		Filter:   EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration: 600 * time.Millisecond,
	})
	if len(got) == 0 || got[0].Content() != "arrived during the gap" {
		t.Fatalf("the gap-fill found nothing: %+v", got)
	}
	if result.Gaps != 0 {
		t.Errorf("gaps = %d, want 0 when the channel is re-readable", result.Gaps)
	}
}

// A workspace-wide stream has no channel list to re-read, so a reconnect
// leaves a hole. Gaps is how a caller learns its stream is not complete.
func TestWatchRecordsGapWhenItCannotRefill(t *testing.T) {
	c, _ := socketFixture(t, mockslack.WSScript{
		Frames:            []map[string]any{mockslack.Hello()},
		HangUpAfterScript: 1,
	})

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

// Slack pushes a pre-authorized reconnect URL routinely, and reconnecting
// through it is the production path — the fallback to client.getWebSocketURL
// is the exception. Neither was exercised.
func TestWatchReconnectsThroughThePushedURL(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	defer ts.Close()
	pushed := mockslack.WebSocketURLFor(ts.URL) + "?frt=fake-reconnect-token"
	server.EnableWebSocket(mockslack.WSScript{
		Frames: []map[string]any{
			mockslack.Hello(),
			{"type": "reconnect_url", "url": pushed},
			mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "before the drop", "1700000015.000100"),
		},
		HangUpAfterScript: 1,
	})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	server.HandleBody("conversations.history", mockslack.History())
	c := browserClientFor(ts.URL)

	_, result := collectWatch(t, c, WatchOptions{
		Filter:   EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration: 600 * time.Millisecond,
	})
	if result.Reconnects != 1 {
		t.Fatalf("reconnects = %d, want 1", result.Reconnects)
	}
	// The redial must have used the pushed URL, which carries its own token.
	conns := server.WSConnections()
	if len(conns) < 2 {
		t.Fatalf("connections = %d, want a reconnect", len(conns))
	}
	if !strings.Contains(conns[1].Query, "frt=fake-reconnect-token") {
		t.Errorf("reconnect query = %q, want the pushed URL's token", conns[1].Query)
	}
}

// A stale pushed URL is normal — they expire. Falling back to a fresh
// client.getWebSocketURL is what keeps a long run alive.
func TestWatchFallsBackWhenThePushedURLIsStale(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	defer ts.Close()
	server.EnableWebSocket(mockslack.WSScript{
		Frames: []map[string]any{
			mockslack.Hello(),
			{"type": "reconnect_url", "url": "ws://127.0.0.1:1/websocket"}, // refuses
		},
		HangUpAfterScript: 1,
	})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	server.HandleBody("conversations.history", mockslack.History())
	c := browserClientFor(ts.URL)

	_, result := collectWatch(t, c, WatchOptions{
		Filter:   EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration: 600 * time.Millisecond,
	})
	if result.Reconnects != 1 {
		t.Errorf("reconnects = %d, want the stale URL to fall back rather than end the run", result.Reconnects)
	}
	if result.StoppedBy == WatchStoppedReconnectFailed {
		t.Error("a stale pushed URL must not fail the run when a refetch works")
	}
}

// A reconnect notice that claims a catch-up which did not happen tells the
// caller their stream is intact when events are missing.
func TestWatchReconnectNoticeReportsWhetherItCaughtUp(t *testing.T) {
	c, _ := socketFixture(t, mockslack.WSScript{
		Frames:            []map[string]any{mockslack.Hello()},
		HangUpAfterScript: 1,
	})

	var filled []bool
	// No channels: a workspace-wide stream has nothing to re-read.
	_, _ = collectWatch(t, c, WatchOptions{
		Filter:      EventFilter{},
		Duration:    600 * time.Millisecond,
		OnReconnect: func(_ int, ok bool) { filled = append(filled, ok) },
	})
	if len(filled) == 0 {
		t.Fatal("expected a reconnect")
	}
	for _, ok := range filled {
		if ok {
			t.Error("a workspace-wide reconnect cannot catch up, and must not claim it did")
		}
	}
}

// A socket that connects and immediately dies is flapping, and the budget must
// retire the run. Every socket is greeted with a hello the instant it opens,
// so counting delivered frames can never tell a flap from a healthy drop.
func TestWatchGivesUpOnAFlappingSocket(t *testing.T) {
	// Never keeps a connection: every dial greets and hangs up.
	c, server := socketFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History())

	_, result := collectWatch(t, c, WatchOptions{
		Filter:   EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration: 5 * time.Second,
	})
	if result.StoppedBy != WatchStoppedReconnectFailed {
		t.Errorf("stopped_by = %q, want %q — a flapping socket must not retry forever",
			result.StoppedBy, WatchStoppedReconnectFailed)
	}
	if result.Reconnects > maxReconnectAttempts+1 {
		t.Errorf("reconnects = %d, want the budget to cap it near %d", result.Reconnects, maxReconnectAttempts)
	}
}

// A refused redial is usually transient. Ending the run on the first one made
// the retry budget unreachable.
func TestWatchRetriesARefusedRedial(t *testing.T) {
	c, server, baseURL := socketFixtureAt(t, mockslack.WSScript{
		Frames:            []map[string]any{mockslack.Hello()},
		HangUpAfterScript: 1,
	})
	// The first redial is sent to a host that refuses; the next one succeeds.
	server.Handle("client.getWebSocketURL",
		mockslack.Response{Body: mockslack.GetWebSocketURL(baseURL)},
		mockslack.Response{Body: map[string]any{"ok": true, "primary_websocket_url": "ws://127.0.0.1:1/websocket"}},
		mockslack.Response{Body: mockslack.GetWebSocketURL(baseURL)},
	)
	server.HandleBody("conversations.history", mockslack.History())

	_, result := collectWatch(t, c, WatchOptions{
		Filter:   EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Duration: 800 * time.Millisecond,
	})
	if result.StoppedBy == WatchStoppedReconnectFailed {
		t.Error("one refused redial must not end the run while attempts remain")
	}
}

// A paged catch-up is consumed in slice order, so it has to be in wire order:
// an await capped at one event must answer with the EARLIEST reply after the
// cursor, not whichever page happened to be fetched first.
func TestWatchBackfillEmitsPagesInChronologicalOrder(t *testing.T) {
	newest := make([]map[string]any, 0, backfillPageLimit)
	for i := range backfillPageLimit {
		newest = append(newest, mockslack.Message(
			fmt.Sprintf("17000005%02d.000100", i), mockslack.WSOtherUser, "newer page"))
	}
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History(newest...)},
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000020.000100", mockslack.WSOtherUser, "the earliest answer"),
		)},
	)

	got, _ := collectWatch(t, c, WatchOptions{
		Filter:    EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		MaxEvents: 1,
		Duration:  3 * time.Second,
	})
	if len(got) != 1 {
		t.Fatalf("got %d events", len(got))
	}
	if got[0].Content() != "the earliest answer" {
		t.Errorf("first event = %q, want the earliest post-cursor message", got[0].Content())
	}
}

// Polling is the entire delivery mechanism on a standard token, and its
// defining behaviour is repetition. Every earlier poll test was satisfied on
// the first pass, so a runPoll that returned instead of looping passed them all.
func TestWatchPollLoopsUntilTheMessageArrives(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History( // the tip read
			mockslack.Message("1700000010.000100", mockslack.WSOtherUser, "already there"),
		)},
		mockslack.Response{Body: mockslack.History()}, // poll 1: nothing yet
		mockslack.Response{Body: mockslack.History()}, // poll 2: still nothing
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000020.000200", mockslack.WSOtherUser, "arrived on the third poll"),
		)},
	)

	got, result := collectWatch(t, c, WatchOptions{
		Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
		Poll:      true,
		PollEvery: 5 * time.Millisecond,
		MaxEvents: 1,
		Duration:  3 * time.Second,
	})
	if len(got) != 1 || got[0].Content() != "arrived on the third poll" {
		t.Fatalf("poll must keep reading until something arrives: %+v", got)
	}
	if result.Cursors[mockslack.WSChannelID] != "1700000020.000200" {
		t.Errorf("cursor = %q, want the delivered message's ts", result.Cursors[mockslack.WSChannelID])
	}
}

// The other half of --idle-timeout: a matching event must restart the
// countdown. With resetIdle a no-op, a busy stream would still be cut off at
// the first interval.
func TestWatchIdleTimeoutIsResetByMatchingEvents(t *testing.T) {
	frames := []map[string]any{mockslack.Hello()}
	for i := range 8 {
		frames = append(frames, mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser,
			"steady traffic", fmt.Sprintf("17000000%02d.000100", 20+i)))
	}
	c, server := watchFixture(t, mockslack.WSScript{Frames: frames, Interval: 20 * time.Millisecond})
	server.HandleBody("conversations.history", mockslack.History())

	got, result := collectWatch(t, c, WatchOptions{
		Filter:      EventFilter{Channels: []string{mockslack.WSChannelID}},
		IdleTimeout: 100 * time.Millisecond,
		MaxEvents:   8,
		Duration:    3 * time.Second,
	})
	if result.StoppedBy == WatchStoppedIdle {
		t.Fatalf("idle tripped after %d events despite steady matching traffic", len(got))
	}
	if len(got) != 8 {
		t.Errorf("delivered %d events, want all 8", len(got))
	}
}

// A permalink target sets ThreadTS, so `message await <permalink> --since` runs
// the thread branch of the backfill — a documented primary path that no test
// had executed.
func TestWatchBackfillReadsAWatchedThread(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.replies", mockslack.History(
		mockslack.ThreadReply("1700000010.000100", mockslack.WSUserID, "the question", "1700000010.000100"),
		mockslack.ThreadReply("1700000020.000200", mockslack.WSOtherUser, "the threaded answer", "1700000010.000100"),
	))

	got, _ := collectWatch(t, c, WatchOptions{
		Filter: EventFilter{
			Since:    "1700000010.000100",
			Channels: []string{mockslack.WSChannelID},
			ThreadTS: "1700000010.000100",
		},
		MaxEvents: 1,
		Duration:  3 * time.Second,
	})
	if len(got) != 1 || got[0].Content() != "the threaded answer" {
		t.Fatalf("thread backfill delivered %+v", got)
	}
	// A thread-scoped run must not fall back to channel history: replies are
	// not in it unless broadcast.
	if len(server.CallsFor("conversations.history")) != 0 {
		t.Error("a thread-scoped backfill should read conversations.replies only")
	}
}

// A broken stdout pipe surfaces here. The event that failed to emit must not
// be counted as delivered.
func TestWatchStopsAndReportsAnEmitFailure(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})
	server.HandleBody("conversations.history", mockslack.History())

	wantErr := errors.New("stdout closed")
	seen := 0
	result, err := Watch(context.Background(), c, WatchOptions{
		Filter:   EventFilter{Channels: []string{mockslack.WSChannelID}},
		Duration: 3 * time.Second,
	}, func(Event) error {
		seen++
		if seen == 2 {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the emit error propagated", err)
	}
	if result.Events != 1 {
		t.Errorf("events = %d, want 1 — the failed emit was never delivered", result.Events)
	}
}
