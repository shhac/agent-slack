package slack

// Conversation bookkeeping for the delivery engine. A reaction frame names
// only the message it is on, so scoping one to a conversation's thread needs
// the thread of every message the run has seen; and a reaction caught up from
// history can also arrive live, so the two copies have to be paired.

import "github.com/shhac/agent-slack/internal/render"

// tracksThreads reports whether the run is scoped to a conversation, which is
// the only case that needs to know a reaction's thread. A workspace stream
// would otherwise index every message it ever sees.
func (s *watchSession) tracksThreads() bool {
	return s.opts.Filter.RepliesTo != "" || s.opts.Filter.ThreadTS != ""
}

// rememberThreads records which thread each message belongs to.
func (s *watchSession) rememberThreads(channelID string, messages ...render.MessageSummary) {
	if !s.tracksThreads() {
		return
	}
	for _, msg := range messages {
		if msg.ThreadTS != "" {
			s.threadOf[threadKey(channelID, msg.TS)] = msg.ThreadTS
		}
	}
}

// withThread fills in a reaction's thread from the message it targets, and
// indexes a message's own thread for the reactions that follow it.
func (s *watchSession) withThread(event Event) Event {
	if event.Kind == EventMessage && event.Message != nil {
		s.rememberThreads(event.ChannelID, *event.Message)
		return event
	}
	if isReactionKind(event.Kind) && event.ThreadTS == "" {
		event.ThreadTS = s.threadOf[threadKey(event.ChannelID, event.TS)]
	}
	return event
}

// isDuplicateReaction pairs a caught-up reaction with its live frame, in
// either order. A socket attached before the backfill can deliver the same
// reaction both ways, and so can a reconnect's gap-fill. A match is consumed,
// and a removal forgets the reaction, so a later re-add still counts.
func (s *watchSession) isDuplicateReaction(event Event) bool {
	key := reactionKey(event)
	switch event.Kind {
	case EventReactionRemoved:
		delete(s.pendingReaction, key)
		return false
	case EventReactionAdded:
		if waiting, ok := s.pendingReaction[key]; ok && waiting != event.CaughtUp {
			delete(s.pendingReaction, key)
			return true
		}
		s.pendingReaction[key] = event.CaughtUp
	}
	return false
}

// threadKey identifies a message for the thread index.
func threadKey(channelID, ts string) string {
	return channelID + "|" + ts
}

// reactionKey identifies a reaction regardless of when it was seen. A
// caught-up copy and its live frame carry different cursors, so eventKey
// cannot collapse them.
func reactionKey(e Event) string {
	return e.ChannelID + "|" + e.TS + "|" + e.Reaction + "|" + e.AuthorID()
}
