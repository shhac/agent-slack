package slack

// What a caller considers an answer. Filtering is deliberately reported, not
// silent: an excluded event still comes back as "skipped", because a filter
// that hides a rejection turns it into a timeout, and an agent cannot tell
// "no" from "no answer".

import "slices"

// EventFilter narrows the classified event stream. A zero filter matches
// message events in any conversation, from anyone but the authenticated user.
type EventFilter struct {
	// Kinds are the event kinds to match; empty means EventMessage only.
	Kinds []EventKind
	// Channels, when non-empty, restricts to these conversation ids.
	Channels []string
	// ThreadTS restricts to one thread. The thread root itself never matches —
	// a caller awaiting in a thread wants replies, not the message it started
	// from — but reactions on the root do.
	ThreadTS string
	// IncludeThreadReplies admits replies to other threads when watching a
	// channel. Off by default so a channel target means what `message list`
	// shows for that channel.
	IncludeThreadReplies bool
	// From, when non-empty, restricts to these author ids (user or bot).
	From []string
	// SelfUserID is the authenticated user, excluded unless IncludeSelf.
	SelfUserID  string
	IncludeSelf bool
	ExcludeBots bool
	// Reactions, when non-empty, restricts reaction events to these names
	// (skin-tone-normalized on both sides).
	Reactions []string
	// Since is an exclusive cursor: events at or before it never match.
	Since string
}

// DefaultKinds is the event set a caller gets without asking: new messages
// only. Edits, deletes, and reactions are opt-in — they are modifications and
// signals, not the answer to "did someone reply".
var DefaultKinds = []EventKind{EventMessage}

// Kind set membership.
func (f EventFilter) kinds() []EventKind {
	if len(f.Kinds) == 0 {
		return DefaultKinds
	}
	return f.Kinds
}

// Matches reports whether an event satisfies the filter.
func (f EventFilter) Matches(e Event) bool {
	if !slices.Contains(f.kinds(), e.Kind) {
		return false
	}
	if len(f.Channels) > 0 && !slices.Contains(f.Channels, e.ChannelID) {
		return false
	}
	if f.Since != "" && !tsAfter(e.Cursor(), f.Since) {
		return false
	}
	if !f.matchesThread(e) {
		return false
	}
	if !f.matchesAuthor(e) {
		return false
	}
	return f.matchesReaction(e)
}

// matchesThread applies the thread scoping. Message events carry their own
// thread_ts; reactions do not, so a reaction is scoped by the message it
// targets — including the thread root, whose approval is the common case.
func (f EventFilter) matchesThread(e Event) bool {
	if f.ThreadTS != "" {
		if isReactionKind(e.Kind) {
			return e.TS == f.ThreadTS || e.ThreadTS == f.ThreadTS
		}
		return e.ThreadTS == f.ThreadTS && e.TS != f.ThreadTS
	}
	if !f.IncludeThreadReplies && e.Kind == EventMessage && e.ThreadTS != "" && e.ThreadTS != e.TS {
		return false
	}
	return true
}

func (f EventFilter) matchesAuthor(e Event) bool {
	if !f.IncludeSelf && f.SelfUserID != "" && e.Author != nil && e.Author.UserID == f.SelfUserID {
		return false
	}
	if f.ExcludeBots && e.IsBot() {
		return false
	}
	if len(f.From) == 0 {
		return true
	}
	return slices.Contains(f.From, e.AuthorID())
}

func (f EventFilter) matchesReaction(e Event) bool {
	if len(f.Reactions) == 0 || !isReactionKind(e.Kind) {
		return true
	}
	got := NormalizeReactionName(e.Reaction)
	for _, want := range f.Reactions {
		if NormalizeReactionName(want) == got {
			return true
		}
	}
	return false
}

// InScope reports whether an event was a candidate answer that the narrowing
// filters excluded — the difference between "worth reporting as skipped" and
// "not what you asked about at all". A rejection in the watched thread is
// worth surfacing; an unrelated channel's traffic, or an event of a kind the
// caller never asked for, is not.
//
// Kind is checked here because it is a primary selector, not a narrowing
// filter: asking for reactions means messages were never candidates, and
// reporting them would bury the one event that matters.
func (f EventFilter) InScope(e Event) bool {
	if !slices.Contains(f.kinds(), e.Kind) {
		return false
	}
	if len(f.Channels) > 0 && !slices.Contains(f.Channels, e.ChannelID) {
		return false
	}
	if f.ThreadTS == "" {
		return true
	}
	return e.TS == f.ThreadTS || e.ThreadTS == f.ThreadTS
}

func isReactionKind(kind EventKind) bool {
	return kind == EventReactionAdded || kind == EventReactionRemoved
}

// tsAfter compares Slack timestamps ("1700000000.000100"). They are fixed-form
// decimal seconds, so a plain string comparison orders them correctly once the
// integer parts are the same length — which they are for any timestamp this
// side of 2286. Lengths are compared first so the ordering survives anyway.
func tsAfter(candidate, cursor string) bool {
	if len(candidate) != len(cursor) {
		return len(candidate) > len(cursor)
	}
	return candidate > cursor
}
