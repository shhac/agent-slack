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
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	// Registered so the poll path COULD succeed: without it, deleting the
	// guard merely swaps this error for the fixture's unknown_method and the
	// test still passes.
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000010.000100", mockslack.WSOtherUser, "anything"),
	))

	_, err := Await(context.Background(), c, AwaitOptions{Poll: true, Timeout: time.Second})
	if err == nil {
		t.Fatal("polling a whole workspace is not viable and must be refused")
	}
	if !strings.Contains(err.Error(), "one conversation") {
		t.Errorf("error = %v, want it to name the real problem", err)
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

// Polling reads conversation history, which contains messages. A caller who
// asked for reactions on a bot token would otherwise wait out the whole
// timeout for something that could never arrive.
func TestAwaitRefusesUnpollableKinds(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	// As above: the poll path must be able to succeed, or this passes for the
	// wrong reason.
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000010.000100", mockslack.WSOtherUser, "anything"),
	))

	_, err := Await(context.Background(), c, AwaitOptions{
		Filter:  EventFilter{Kinds: []EventKind{EventReactionAdded}, Channels: []string{mockslack.WSChannelID}},
		Poll:    true,
		Timeout: time.Second,
	})
	if err == nil {
		t.Fatal("polling cannot deliver reactions and must say so")
	}
	if !strings.Contains(err.Error(), "reaction") {
		t.Errorf("error = %v, want it to name the kind it cannot deliver", err)
	}
}

// A lost socket must not read as a clean timeout — the caller decides whether
// to resume from that distinction.
func TestAwaitReportsWhyItStopped(t *testing.T) {
	c, server := socketFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History())

	result, err := Await(context.Background(), c, AwaitOptions{
		Filter:  EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Received {
		t.Fatal("nothing was sent")
	}
	if result.StoppedBy != WatchStoppedReconnectFailed {
		t.Errorf("stopped_by = %q, want the lost socket reported rather than a bare timeout", result.StoppedBy)
	}
}

// --idle-timeout is a documented bound and `message stream` accepts it as the
// only one. A poll loop that ignored it would run forever against a rate limit
// — the exact failure the bound check exists to prevent.
func TestPollHonoursTheIdleTimeout(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000010.000100", mockslack.WSOtherUser, "the tip"),
	))

	_, result := collectWatch(t, c, WatchOptions{
		Filter:      EventFilter{Channels: []string{mockslack.WSChannelID}},
		Poll:        true,
		PollEvery:   time.Millisecond,
		IdleTimeout: 50 * time.Millisecond,
		Duration:    3 * time.Second, // generous: idle is what must stop this
	})
	if result.StoppedBy != WatchStoppedIdle {
		t.Errorf("stopped_by = %q, want %q — an idle-only poll must still terminate",
			result.StoppedBy, WatchStoppedIdle)
	}
}

// A poll run that matched nothing still established a floor. Losing it makes
// the next run re-baseline at the tip and skip whatever arrived between them.
func TestPollTimeoutReturnsTheBaselineAsItsCursor(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000010.000100", mockslack.WSOtherUser, "the tip"),
	))

	result, err := Await(context.Background(), c, AwaitOptions{
		Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
		Poll:      true,
		PollEvery: time.Millisecond,
		Timeout:   120 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("a poll timeout is not an error: %v", err)
	}
	if result.Received {
		t.Fatal("nothing new arrived")
	}
	if result.Cursor != "1700000010.000100" {
		t.Errorf("cursor = %q, want the established baseline so the next run resumes from it", result.Cursor)
	}
}

// A broken writer must never be reported as a clean timeout, however the run's
// clock happened to fall.
func TestPollEmitFailureIsNotSwallowedAsATimeout(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History()},
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000020.000200", mockslack.WSOtherUser, "delivered"),
		)},
	)

	wantErr := errors.New("stdout closed")
	_, err := Watch(context.Background(), c, WatchOptions{
		Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
		Poll:      true,
		PollEvery: time.Millisecond,
		Duration:  150 * time.Millisecond,
	}, func(Event) error {
		// Fail AFTER the run's deadline, so the write error and the expiry
		// coincide — the only situation where the swallow could hide it.
		time.Sleep(250 * time.Millisecond)
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the emit failure surfaced rather than swallowed by the deadline", err)
	}
}

// The idle countdown measures the RUN. A timer recreated per connection never
// trips on a socket that drops more often than --idle-timeout, so a stream
// bounded only by idle would run to its duration or forever.
func TestIdleTimerSurvivesReconnects(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	defer ts.Close()
	// Keeps dropping for the whole run, faster than the idle interval.
	server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	server.HandleBody("conversations.history", mockslack.History())
	// A short but real reconnect pause, so drops are paced rather than instant:
	// with a no-op sleep the run would exhaust its retry budget before idle.
	c := New(Auth{Type: AuthBrowser, XOXC: "xoxc-s", XOXD: "xoxd-s", WorkspaceURL: ts.URL},
		WithSleep(func(ctx context.Context, _ time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(15 * time.Millisecond):
				return nil
			}
		}))

	_, result := collectWatch(t, c, WatchOptions{
		Filter:      EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
		IdleTimeout: 120 * time.Millisecond,
		Duration:    4 * time.Second,
	})
	if result.Reconnects == 0 {
		t.Fatal("expected the socket to drop repeatedly")
	}
	if result.StoppedBy != WatchStoppedIdle {
		t.Errorf("stopped_by = %q, want %q — the countdown must span reconnects",
			result.StoppedBy, WatchStoppedIdle)
	}
}

// A permalink await with --poll baselines from the THREAD, not the channel: a
// channel-derived tip is older than the thread's replies, so the first poll
// would replay them.
func TestPollBaselineUsesTheWatchedThread(t *testing.T) {
	c, server := watchFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}})
	server.HandleBody("conversations.replies", mockslack.History(
		mockslack.ThreadReply("1700000010.000100", mockslack.WSUserID, "the question", "1700000010.000100"),
		mockslack.ThreadReply("1700000020.000200", mockslack.WSOtherUser, "already answered", "1700000010.000100"),
	))

	got, _ := collectWatch(t, c, WatchOptions{
		Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}, ThreadTS: "1700000010.000100"},
		Poll:      true,
		PollEvery: time.Millisecond,
		Duration:  400 * time.Millisecond,
	})
	if len(got) != 0 {
		t.Errorf("baselining off the thread means its existing replies are not new: %+v", got)
	}
	if calls := len(server.CallsFor("conversations.history")); calls != 0 {
		t.Errorf("a thread-scoped poll made %d channel-history calls", calls)
	}
}

// --poll-interval is a request rate against a rate-limited endpoint, so an
// aggressive value is floored rather than honoured verbatim.
func TestPollIntervalHasAFloor(t *testing.T) {
	var slept []time.Duration
	server := mockslack.New()
	ts := httptest.NewServer(server)
	defer ts.Close()
	server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}, KeepOpen: true})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000010.000100", mockslack.WSOtherUser, "the tip"),
	))
	c := New(Auth{Type: AuthBrowser, XOXC: "xoxc-s", XOXD: "xoxd-s", WorkspaceURL: ts.URL},
		WithSleep(func(ctx context.Context, d time.Duration) error {
			slept = append(slept, d)
			return ctx.Err()
		}))

	_, _ = Watch(context.Background(), c, WatchOptions{
		Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
		Poll:      true,
		PollEvery: time.Millisecond, // far below the floor
		Duration:  300 * time.Millisecond,
	}, func(Event) error { return nil })

	if len(slept) == 0 {
		t.Fatal("the poll loop never paced itself")
	}
	for _, d := range slept {
		if d < minPollEvery {
			t.Fatalf("slept %s, below the %s floor — an aggressive --poll-interval must be clamped", d, minPollEvery)
		}
	}
}
