package slack

// What a caller considers an answer. Filtering is deliberately reported, not
// silent: an excluded event still comes back as "skipped", because a filter
// that hides a rejection turns it into a timeout, and an agent cannot tell
// "no" from "no answer".

import (
	"slices"
	"strconv"
	"strings"
)

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
	// RepliesTo is the message the caller is awaiting answers to. Replies
	// threaded on it match even when watching a channel, because a human
	// answering a question picks in-thread or in-channel unpredictably and
	// both are the answer. Without this an await on a channel misses exactly
	// the reply it was posted to collect.
	RepliesTo string
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
	if e.Kind != EventMessage || e.ThreadTS == "" || e.ThreadTS == e.TS {
		return true
	}
	return f.IncludeThreadReplies || (f.RepliesTo != "" && e.ThreadTS == f.RepliesTo)
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

// tsAfter reports whether candidate is strictly later than cursor.
//
// Slack timestamps are "<seconds>.<micros>", but a cursor can reach us from a
// caller rather than the wire — `--since 1700000000` or a value with fewer
// micro digits — so the two sides are not always the same shape. Comparing
// them as strings (or by length) inverts the ordering whenever the shapes
// differ, which silently makes a filter match everything or nothing. Parse and
// compare numerically instead.
func tsAfter(candidate, cursor string) bool {
	candSec, candMicro := splitTS(candidate)
	curSec, curMicro := splitTS(cursor)
	if candSec != curSec {
		return candSec > curSec
	}
	return candMicro > curMicro
}

// splitTS parses "<seconds>.<micros>" into its two integer parts. Micros are
// right-padded to six digits so ".1" and ".100000" compare equal, and any
// unparseable part reads as 0 — a malformed timestamp sorts earliest rather
// than randomly.
func splitTS(ts string) (seconds, micros int64) {
	secPart, microPart, _ := strings.Cut(strings.TrimSpace(ts), ".")
	seconds, _ = strconv.ParseInt(secPart, 10, 64)
	if microPart == "" {
		return seconds, 0
	}
	if len(microPart) > 6 {
		microPart = microPart[:6]
	}
	micros, _ = strconv.ParseInt(microPart+strings.Repeat("0", 6-len(microPart)), 10, 64)
	return seconds, micros
}

// maxTS returns whichever timestamp is later, treating empty as "unset". It is
// the one place cursors advance, so a high-water mark can never move backwards.
func maxTS(current, candidate string) string {
	if candidate == "" {
		return current
	}
	if current == "" || tsAfter(candidate, current) {
		return candidate
	}
	return current
}
