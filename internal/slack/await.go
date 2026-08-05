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
	Filter    EventFilter
	ChannelID string
	ThreadTS  string
	Timeout   time.Duration
	Poll      bool
	PollEvery time.Duration
	PingEvery time.Duration
	// MaxSkipped bounds the excluded-event report. Zero uses the default.
	MaxSkipped  int
	OnReconnect func(attempt int)
}

// AwaitResult is the single JSON resource `message await` prints. Received is
// false on a clean timeout, which is a successful outcome, not an error.
type AwaitResult struct {
	Received bool   `json:"received"`
	Cursor   string `json:"cursor,omitempty"`
	WaitedMS int64  `json:"waited_ms"`
	Event    *Event `json:"event,omitempty"`
	// Skipped are in-scope events the filter excluded — a "no" the caller would
	// otherwise read as silence.
	Skipped []Event `json:"skipped,omitempty"`
	// SkippedTruncated reports that more were excluded than could be listed.
	// The cursor stops before them, so resuming re-offers them.
	SkippedTruncated bool `json:"skipped_truncated,omitempty"`
	Reconnects       int  `json:"reconnects,omitempty"`
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
	result, err := Watch(ctx, c, WatchOptions{
		Filter:           opts.Filter,
		BackfillChannel:  opts.ChannelID,
		BackfillThreadTS: opts.ThreadTS,
		Duration:         opts.Timeout,
		MaxEvents:        1,
		PingEvery:        opts.PingEvery,
		Poll:             opts.Poll,
		PollEvery:        opts.PollEvery,
		OnReconnect:      opts.OnReconnect,
		MaxSkipped:       maxSkipped,
		OnSkipped:        func(event Event) { skipped = append(skipped, event) },
	}, func(event Event) error {
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
		Cursor:           FirstNonEmpty(result.Cursors[opts.ChannelID], opts.Filter.Since),
		WaitedMS:         time.Since(started).Milliseconds(),
		Event:            matched,
		Skipped:          skipped,
		SkippedTruncated: result.SkippedTruncated,
		Reconnects:       result.Reconnects,
	}, nil
}
