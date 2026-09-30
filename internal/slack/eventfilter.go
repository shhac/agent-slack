package slack

// What a caller considers an answer. Filtering is deliberately reported, not
// silent: an excluded event still comes back as "skipped", because a filter
// that hides a rejection turns it into a timeout, and an agent cannot tell
// "no" from "no answer".

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/shhac/agent-slack/internal/render"
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
	// RepliesTo is the root of the conversation the caller is holding. Replies
	// threaded on it match even when watching a channel, because a human
	// answering a question picks in-thread or in-channel unpredictably and
	// both are the answer. Without this an await on a channel misses exactly
	// the reply it was posted to collect. It also scopes reactions: see
	// reactsWithinConversation.
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
//
// A filter has two halves. The PRIMARY SELECTORS — kind, conversation, thread,
// cursor — say what the caller is watching at all. The NARROWING FILTERS —
// author, bots, reaction name — say which of those count as an answer. Only
// the second kind produces a "skipped" report, because only there can an
// excluded event still be news (a rejection). Splitting them this way makes
// Matches imply InScope by construction rather than by two lists kept in sync.
func (f EventFilter) Matches(e Event) bool { return f.InScope(e) && f.narrows(e) }

// InScope reports whether an event was a candidate at all — the difference
// between "excluded, worth reporting as skipped" and "not what you asked
// about". A rejection on the watched message is worth surfacing; another
// channel's traffic, or a kind the caller never asked for, is not.
func (f EventFilter) InScope(e Event) bool {
	if !slices.Contains(f.kinds(), e.Kind) {
		return false
	}
	if len(f.Channels) > 0 && !slices.Contains(f.Channels, e.ChannelID) {
		return false
	}
	if f.Since != "" && !tsAfter(e.Cursor(), f.Since) {
		return false
	}
	// Your own activity is never an answer, so it is out of scope rather
	// than "skipped": the skipped report exists to surface a "no" that would
	// read as silence, and a conversation's own replies would bury it.
	if f.isSelf(e) {
		return false
	}
	return f.matchesThread(e)
}

func (f EventFilter) isSelf(e Event) bool {
	return !f.IncludeSelf && f.SelfUserID != "" && e.Author != nil && e.Author.UserID == f.SelfUserID
}

// narrows applies the filters that decide which in-scope events are answers.
func (f EventFilter) narrows(e Event) bool {
	return f.matchesAuthor(e) && f.matchesReaction(e)
}

// matchesThread dispatches between the two thread-scoping policies, which have
// nothing in common beyond the field that selects them.
func (f EventFilter) matchesThread(e Event) bool {
	if f.ThreadTS != "" {
		return f.inWatchedThread(e)
	}
	return f.inChannelScope(e)
}

// inWatchedThread scopes a run pinned to one thread. A reaction is scoped by
// the message it targets: the thread root, since approving the message that
// started the thread is the common case, or one of the caller's own replies
// in it. The root message itself never matches: awaiting in a thread means
// replies.
func (f EventFilter) inWatchedThread(e Event) bool {
	if isReactionKind(e.Kind) {
		return e.TS == f.ThreadTS || (f.onOwnMessage(e) && e.ThreadTS == f.ThreadTS)
	}
	return e.ThreadTS == f.ThreadTS && e.TS != f.ThreadTS
}

// inChannelScope scopes a run watching a conversation. Replies inside other
// threads are excluded, matching what `message list <channel>` shows — except
// replies to the message the caller is awaiting answers to, which are exactly
// what they asked for.
func (f EventFilter) inChannelScope(e Event) bool {
	if isReactionKind(e.Kind) {
		return f.RepliesTo == "" || f.reactsWithinConversation(e)
	}
	isThreadReply := e.Kind == EventMessage && e.ThreadTS != "" && e.ThreadTS != e.TS
	if !isThreadReply {
		return true
	}
	return f.IncludeThreadReplies || f.inConversationThread(e.ThreadTS)
}

// inConversationThread reports a thread that belongs to the conversation: the
// root's own, or one started on a message posted after the root. Answering
// someone by threading on *their* reply is still the same conversation, and a
// thread on a message from before the root is not.
func (f EventFilter) inConversationThread(threadTS string) bool {
	return f.RepliesTo != "" && compareTS(threadTS, f.RepliesTo) >= 0
}

// reactsWithinConversation decides whether a reaction answers the caller.
// Anyone reacting to someone else's message is not talking to the caller, and
// under browser auth "self" is a person who also posts elsewhere in the
// channel — so a reaction on the caller's message counts only when that
// message is part of this conversation: the root, a reply in one of its
// threads, or something the caller posted after --since. A reaction's thread is known
// only when the watch has seen the message it targets; an unknown one falls
// back to the --since bound.
func (f EventFilter) reactsWithinConversation(e Event) bool {
	if e.TS == f.RepliesTo {
		return true
	}
	if !f.onOwnMessage(e) {
		return false
	}
	if e.ThreadTS != "" {
		return f.inConversationThread(e.ThreadTS)
	}
	return compareTS(e.TS, f.RepliesTo) >= 0 && compareTS(e.TS, f.Since) >= 0
}

// onOwnMessage reports a reaction on a message the caller wrote.
func (f EventFilter) onOwnMessage(e Event) bool {
	return f.SelfUserID != "" && e.TargetAuthor == f.SelfUserID
}

func (f EventFilter) matchesAuthor(e Event) bool {
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
	// Both sides are stripped: the wire carries the reactor's skin tone, and a
	// filter value may arrive un-normalized from an engine-level caller.
	got := render.StripSkinTone(e.Reaction)
	for _, want := range f.Reactions {
		if render.StripSkinTone(want) == got {
			return true
		}
	}
	return false
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
	return compareTS(candidate, cursor) > 0
}

// compareTS orders two timestamps numerically: -1, 0, or +1.
func compareTS(a, b string) int {
	aSec, aMicro := splitTS(a)
	bSec, bMicro := splitTS(b)
	if c := cmp.Compare(aSec, bSec); c != 0 {
		return c
	}
	return cmp.Compare(aMicro, bMicro)
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

// justAfter is the smallest timestamp strictly later than ts.
func justAfter(ts string) string {
	seconds, micros := splitTS(ts)
	micros++
	if micros == 1_000_000 {
		seconds, micros = seconds+1, 0
	}
	return fmt.Sprintf("%d.%06d", seconds, micros)
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
