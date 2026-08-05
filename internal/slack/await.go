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
	Skipped    []Event `json:"skipped,omitempty"`
	Reconnects int     `json:"reconnects,omitempty"`
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
	// examined tracks the high-water mark of everything we looked at, matched
	// or not, so a resumed run does not re-offer events already reported.
	examined := opts.Filter.Since

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
		OnSkipped: func(event Event) {
			examined = laterTS(examined, event.Cursor())
			if len(skipped) < maxSkipped {
				skipped = append(skipped, event)
			}
		},
	}, func(event Event) error {
		matched = &event
		examined = laterTS(examined, event.Cursor())
		return nil
	})
	if err != nil {
		return AwaitResult{}, err
	}

	return AwaitResult{
		Received:   matched != nil,
		Cursor:     examined,
		WaitedMS:   time.Since(started).Milliseconds(),
		Event:      matched,
		Skipped:    skipped,
		Reconnects: result.Reconnects,
	}, nil
}

// laterTS returns whichever timestamp is later, treating empty as "unset".
func laterTS(current, candidate string) string {
	if candidate == "" {
		return current
	}
	if current == "" || tsAfter(candidate, current) {
		return candidate
	}
	return current
}
