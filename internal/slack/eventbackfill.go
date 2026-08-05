package slack

// Catching up over HTTP. The socket only delivers what happens while it is
// attached, so two gaps need conversations.history: the window between a
// caller's --since cursor and the moment we connect, and whatever a dropped
// connection missed before it was re-established.

import (
	"context"

	"github.com/shhac/agent-slack/internal/render"
)

const (
	// backfillPageLimit is one conversations.history page; backfillMaxMessages
	// bounds a catch-up so a very stale cursor cannot pull an unbounded read.
	backfillPageLimit   = 200
	backfillMaxMessages = 1000
)

// backfill catches up the single conversation the caller named, so a reply
// that arrived between sending and waiting is not missed.
func (s *watchSession) backfill(ctx context.Context) error {
	channel := s.opts.targetChannel()
	if channel == "" || s.opts.Filter.Since == "" {
		return nil
	}
	return s.backfillChannel(ctx, channel, s.opts.Filter.ThreadTS, s.opts.Filter.Since)
}

// backfillChannel replays history after a cursor through the same offer path
// as live frames, so dedup and filtering behave identically.

func (s *watchSession) backfillChannel(ctx context.Context, channelID, threadTS, since string) error {
	if since == "" {
		return nil
	}
	messages, err := s.fetchSince(ctx, channelID, threadTS, since)
	if err != nil {
		return err
	}
	for _, msg := range messages {
		_, done, emitErr := s.offer(EventFromMessage(channelID, msg))
		if emitErr != nil {
			return emitErr
		}
		if done {
			return nil
		}
	}
	return nil
}

// fetchSince reads a thread's replies or a channel's history after a cursor.
// A thread's replies never appear in channel history unless broadcast, so the
// two cases genuinely need different calls.

func (s *watchSession) fetchSince(ctx context.Context, channelID, threadTS, since string) ([]render.MessageSummary, error) {
	if threadTS != "" {
		replies, err := FetchThread(ctx, s.client, channelID, threadTS, false)
		if err != nil {
			return nil, err
		}
		return orderedBackfill(afterCursor(replies, since)), nil
	}
	messages, err := s.historySince(ctx, channelID, since)
	if err != nil {
		return nil, err
	}
	out := afterCursor(messages, since)

	// A reply threaded on the awaited message is not in channel history unless
	// it was broadcast, so it needs its own read — otherwise the backfill
	// misses the very answer RepliesTo exists to catch.
	repliesTo := s.opts.Filter.RepliesTo
	if repliesTo == "" {
		return orderedBackfill(out), nil
	}
	// Best-effort, unlike the channel read: --since may be a cursor from an
	// earlier run rather than a message the caller posted, in which case there
	// is no thread to read. Failing the whole await over a speculative fetch
	// would be worse than losing in-thread replies from before it started —
	// the live socket still delivers them from here on.
	replies, err := FetchThread(ctx, s.client, channelID, repliesTo, false)
	if err != nil {
		s.client.debugf("replies backfill for %s skipped: %v", repliesTo, err)
		return orderedBackfill(out), nil
	}
	return orderedBackfill(append(out, afterCursor(replies, since)...)), nil
}

// historySince reads every message after a cursor, following pages rather than
// stopping at the first. A single page silently truncates a catch-up from a
// stale cursor — the caller would be told it had missed nothing.

func (s *watchSession) historySince(ctx context.Context, channelID, since string) ([]render.MessageSummary, error) {
	var all []render.MessageSummary
	latest := ""
	for len(all) < backfillMaxMessages {
		page, err := FetchChannelHistory(ctx, s.client, HistoryOptions{
			ChannelID: channelID,
			Limit:     backfillPageLimit,
			Oldest:    since,
			Latest:    latest,
		})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return all, nil
		}
		all = append(all, page...)
		if len(page) < backfillPageLimit {
			return all, nil
		}
		// Pages run newest-first; step back from the oldest message we hold.
		// Overlap at the boundary is harmless — dedup collapses it.
		latest = page[0].TS
	}
	// Hitting the cap means older post-cursor messages were not read. That is
	// exactly what Gaps reports: events may be missing.
	s.result.Gaps++
	return all, nil
}

// orderedBackfill puts a multi-source catch-up into wire order. Pages are
// fetched newest-window-first and the thread read is appended after the
// channel read, so the raw slice is only chronological *within* each block —
// and an await capped at one event would answer with whichever block came
// first rather than the earliest reply.
func orderedBackfill(messages []render.MessageSummary) []render.MessageSummary {
	sortChronological(messages)
	return messages
}

// afterCursor enforces the exclusive semantics of --since: Slack's `oldest` is
// inclusive, and the cursor is usually the caller's own message.

func afterCursor(messages []render.MessageSummary, since string) []render.MessageSummary {
	out := make([]render.MessageSummary, 0, len(messages))
	for _, msg := range messages {
		if tsAfter(msg.TS, since) {
			out = append(out, msg)
		}
	}
	return out
}

// runPoll is the standard-token fallback: no socket, just history reads after
// the cursor. Only usable with a single named conversation.
