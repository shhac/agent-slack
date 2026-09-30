package slack

// Catching up over HTTP. The socket only delivers what happens while it is
// attached, so two gaps need conversations.history: the window between a
// caller's --since cursor and the moment we connect, and whatever a dropped
// connection missed before it was re-established.

import (
	"context"
	"slices"

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
	s.rememberThreads(channelID, messages...)
	for _, event := range catchUpEvents(channelID, messages, since) {
		if _, emitErr := s.offer(event); emitErr != nil {
			return emitErr
		}
		if s.stopped() {
			return nil
		}
	}
	return nil
}

// catchUpEvents turns a catch-up read into events in cursor order: every
// message after the cursor, plus the reactions on every message at or after
// it (the filter drops them when reactions were not asked for). History does
// not date a reaction, but one on a message posted at or after --since cannot
// predate --since, so it is new. Reactions on older messages cannot be told apart
// from ones that were already there, and are left to the live socket.
func catchUpEvents(channelID string, messages []render.MessageSummary, since string) []Event {
	var events []Event
	for _, msg := range messages {
		if tsAfter(msg.TS, since) {
			events = append(events, EventFromMessage(channelID, msg))
		}
		if compareTS(msg.TS, since) >= 0 {
			events = append(events, caughtUpReactions(channelID, msg)...)
		}
	}
	// Pages are fetched newest-window-first and the thread read is appended
	// after the channel read, so the slice is only chronological within each
	// block — and an await capped at one event would answer with whichever
	// block came first rather than the earliest reply.
	slices.SortStableFunc(events, func(a, b Event) int { return compareTS(a.Cursor(), b.Cursor()) })
	return events
}

// caughtUpReactions reads a message's reactions back as events (see
// Event.Cursor for where they sit).
func caughtUpReactions(channelID string, msg render.MessageSummary) []Event {
	var events []Event
	for _, reaction := range render.CompactReactions(msg.Reactions) {
		for _, user := range reaction.Users {
			events = append(events, Event{
				Kind:            EventReactionAdded,
				ChannelID:       channelID,
				TS:              msg.TS,
				ThreadTS:        msg.ThreadTS,
				Author:          render.AuthorRef(user, ""),
				Reaction:        reaction.Name,
				TargetAuthor:    msg.User,
				CaughtUp:        true,
				TargetReactions: msg.Reactions,
			})
		}
	}
	return events
}

// fetchSince reads a thread's replies, or a channel's history from the cursor
// on, including the message at the cursor itself: it is usually the caller's
// own, and the reactions on it are what an await is waiting for. A thread's
// replies never appear in channel history unless broadcast, so the two cases
// genuinely need different calls. The result is unfiltered; catchUpEvents
// applies the cursor.
func (s *watchSession) fetchSince(ctx context.Context, channelID, threadTS, since string) ([]render.MessageSummary, error) {
	if threadTS != "" {
		return FetchThread(ctx, s.client, channelID, threadTS, false)
	}
	repliesTo := s.opts.Filter.RepliesTo
	if repliesTo == "" {
		return s.historySince(ctx, channelID, since)
	}
	// A conversation's answers can sit in threads on any of its messages, and
	// a thread reply is absent from channel history unless broadcast. So read
	// from the root rather than the cursor — a new reply can land in a thread
	// on a message from well before it — and then each thread with news.
	messages, err := s.historySince(ctx, channelID, earlierTS(repliesTo, since))
	if err != nil {
		return nil, err
	}
	for _, root := range s.threadsWithNews(messages, repliesTo, since) {
		// Best-effort, unlike the channel read: --since may be a cursor from
		// an earlier run rather than a message that started a thread. Failing
		// the whole await over a speculative fetch would be worse than losing
		// in-thread replies from before it started — the live socket still
		// delivers them from here on.
		replies, err := FetchThread(ctx, s.client, channelID, root, false)
		if err != nil {
			s.client.debugf("replies backfill for %s skipped: %v", root, err)
			continue
		}
		messages = append(messages, replies...)
	}
	return messages, nil
}

// maxCatchUpThreads bounds the thread reads one catch-up makes, so a long
// conversation in a busy channel cannot fan out into a request per thread.
const maxCatchUpThreads = 20

// threadsWithNews picks the conversation's threads worth reading: always the
// root's, plus every thread started on a later message whose newest reply is
// after the cursor. Past the bound the rest are dropped and counted as a gap.
func (s *watchSession) threadsWithNews(messages []render.MessageSummary, repliesTo, since string) []string {
	roots := []string{repliesTo}
	for _, msg := range messages {
		startsThread := msg.ReplyCount > 0 && (msg.ThreadTS == "" || msg.ThreadTS == msg.TS)
		if !startsThread || !tsAfter(msg.TS, repliesTo) {
			continue
		}
		if msg.LatestReply != "" && !tsAfter(msg.LatestReply, since) {
			continue
		}
		roots = append(roots, msg.TS)
	}
	if len(roots) > maxCatchUpThreads {
		s.result.Gaps++
		roots = roots[:maxCatchUpThreads]
	}
	return roots
}

// earlierTS returns whichever timestamp is earlier.
func earlierTS(a, b string) string {
	if compareTS(a, b) <= 0 {
		return a
	}
	return b
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
			Inclusive: true,
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
