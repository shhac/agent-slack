package slack

// The standard-token fallback. client.getWebSocketURL is a client API, so a
// bot or user token cannot open the event socket at all; repeated history
// reads deliver the same events, just later and against a rate limit.

import (
	"context"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

const (
	defaultPollEvery = 15 * time.Second
	// minPollEvery keeps an aggressive --poll-interval from becoming a request
	// storm against a rate-limited endpoint.
	minPollEvery = 250 * time.Millisecond
)

// runPoll is the standard-token fallback: no socket, just history reads after
// the cursor. Only usable with a single named conversation.
func (s *watchSession) runPoll(ctx context.Context) error {
	if kind, ok := s.unpollableKind(); ok {
		return agenterrors.Newf(agenterrors.FixableByAgent,
			"%s events are only delivered over the event socket, and polling reads message history", kind).
			WithHint("drop --poll to use the socket (browser auth), or await messages only")
	}
	channel := s.opts.targetChannel()
	if channel == "" {
		return agenterrors.New(
			"polling reads one conversation at a time; watching a whole workspace needs the event socket",
			agenterrors.FixableByAgent).
			WithHint("name exactly one conversation, or use the event socket (browser auth)")
	}
	every := s.opts.PollEvery
	if every <= 0 {
		every = defaultPollEvery
	}
	// A floor, because the cadence is a request rate: nothing stops a caller
	// asking for 1ms, and conversations.history is rate-limited.
	every = max(every, minPollEvery)
	cursor, err := s.pollBaseline(ctx)
	if err != nil {
		return err
	}

	// The baseline is a real resume point even when nothing matches: without
	// recording it, a timed-out await returns no cursor and the next run
	// re-baselines at the tip, silently skipping whatever arrived in between.
	// "0" is synthetic (an empty conversation) and anchors nothing.
	if cursor != "" && cursor != "0" {
		s.result.Cursors[channel] = maxTS(s.result.Cursors[channel], cursor)
		s.watermark[channel] = maxTS(s.watermark[channel], cursor)
	}

	lastEvent := time.Now()
	for {
		delivered := s.result.Events
		if err := s.backfillChannel(ctx, channel, s.opts.Filter.ThreadTS, cursor); err != nil {
			return err
		}
		if s.stopped() {
			return nil
		}
		cursor = maxTS(cursor, s.watermark[channel])

		// --idle-timeout is a documented bound, and `message stream` accepts it
		// as the *only* bound. Without honouring it here a polled run would
		// loop forever against a rate limit — the exact thing the bound check
		// exists to prevent.
		if s.result.Events > delivered {
			lastEvent = time.Now()
		}
		if s.opts.IdleTimeout > 0 && time.Since(lastEvent) >= s.opts.IdleTimeout {
			s.stop(WatchStoppedIdle)
			return nil
		}
		if err := s.client.sleep(ctx, every); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// unpollableKind reports an event kind the poll fallback cannot produce.
// Polling reads conversation history, which contains messages — a caller
// awaiting a reaction would otherwise wait out the whole timeout for something
// that could never arrive.
func (s *watchSession) unpollableKind() (EventKind, bool) {
	for _, kind := range s.opts.Filter.kinds() {
		if kind != EventMessage {
			return kind, true
		}
	}
	return "", false
}

// pollBaseline is where a poll run starts reading from. With no --since there
// is no cursor, and "everything in history" is the wrong answer — the caller
// asked what happens next — so the conversation's current tip becomes the
// baseline.
func (s *watchSession) pollBaseline(ctx context.Context) (string, error) {
	if s.opts.Filter.Since != "" {
		return s.opts.Filter.Since, nil
	}
	messages, err := s.fetchTip(ctx)
	if err != nil {
		return "", err
	}
	if len(messages) == 0 {
		// An empty conversation has no tip to start from, and an empty cursor
		// makes every later read a no-op — the poll would spin until timeout
		// and report silence even as messages arrived. "0" is the right
		// baseline: the read that established emptiness already proved there
		// is nothing to replay, and unlike a wall-clock stamp it cannot be
		// skewed against Slack's own timestamps.
		return "0", nil
	}
	return messages[len(messages)-1].TS, nil
}

func (s *watchSession) fetchTip(ctx context.Context) ([]render.MessageSummary, error) {
	channel := s.opts.targetChannel()
	if s.opts.Filter.ThreadTS != "" {
		return FetchThread(ctx, s.client, channel, s.opts.Filter.ThreadTS, false)
	}
	return FetchChannelHistory(ctx, s.client, HistoryOptions{ChannelID: channel, Limit: 1})
}
