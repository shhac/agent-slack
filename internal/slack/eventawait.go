package slack

// Waiting for one answer. Await is Watch bounded to a single matching event,
// plus the bookkeeping that makes a timeout useful: a cursor the caller can
// resume from, and the in-scope events the filter excluded — without which a
// rejection is indistinguishable from silence.

import (
	"context"
	"time"
)

// AwaitOptions configures one await.
type AwaitOptions struct {
	// Filter names the conversation (and thread) to await in, via its Channels
	// and ThreadTS. The engine reads the target from there rather than from a
	// second set of fields that could disagree with it.
	Filter    EventFilter
	Timeout   time.Duration
	Poll      bool
	PollEvery time.Duration
	PingEvery time.Duration
	// MaxSkipped bounds the excluded-event report. Zero uses the default.
	MaxSkipped  int
	OnReconnect func(attempt int, filled bool)
}

// AwaitResult is what one await produced. Received is false on a clean
// timeout, which is a successful outcome, not an error. Like Event this is the
// engine's shape, not the wire's — the CLI projects it into awaitOutput.
type AwaitResult struct {
	Received bool
	Cursor   string
	WaitedMS int64
	Event    *Event
	// Skipped are in-scope events the filter excluded — a "no" the caller would
	// otherwise read as silence.
	Skipped []Event
	// SkippedTruncated reports that more were excluded than could be listed.
	// The cursor stops before them, so resuming re-offers them.
	SkippedTruncated bool
	Reconnects       int
	// StoppedBy is why the wait ended. Without it a lost socket is
	// indistinguishable from a clean timeout, and the caller cannot tell
	// "nobody answered" from "I stopped listening".
	StoppedBy string
	// Gaps counts catch-ups that could not be completed; non-zero means the
	// answer may have arrived unseen.
	Gaps int
}

const defaultMaxSkipped = 20

// Await blocks until one event matches, the timeout elapses, or the context is
// cancelled. A timeout is not an error: it returns Received=false with the
// cursor to resume from.
func Await(ctx context.Context, c *Client, opts AwaitOptions) (AwaitResult, error) {
	maxSkipped := opts.MaxSkipped
	if maxSkipped == 0 {
		maxSkipped = defaultMaxSkipped
	}

	var matched *Event
	var skipped []Event

	started := time.Now()
	watchOpts := WatchOptions{
		Filter:      opts.Filter,
		Duration:    opts.Timeout,
		MaxEvents:   1,
		PingEvery:   opts.PingEvery,
		Poll:        opts.Poll,
		PollEvery:   opts.PollEvery,
		OnReconnect: opts.OnReconnect,
		MaxSkipped:  maxSkipped,
		OnSkipped:   func(event Event) { skipped = append(skipped, event) },
	}
	result, err := Watch(ctx, c, watchOpts, func(event Event) error {
		matched = &event
		return nil
	})
	if err != nil {
		return AwaitResult{}, err
	}

	return AwaitResult{
		Received: matched != nil,
		// The session's high-water mark for this conversation: everything
		// examined, never past anything unreported. Falls back to the input so
		// a run that saw nothing echoes its cursor rather than losing it.
		Cursor:           FirstNonEmpty(result.Cursors[watchOpts.targetChannel()], opts.Filter.Since),
		WaitedMS:         time.Since(started).Milliseconds(),
		Event:            matched,
		Skipped:          skipped,
		SkippedTruncated: result.SkippedTruncated,
		Reconnects:       result.Reconnects,
		StoppedBy:        result.StoppedBy,
		Gaps:             result.Gaps,
	}, nil
}
