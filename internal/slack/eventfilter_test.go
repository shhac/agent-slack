package slack

import (
	"testing"

	"github.com/shhac/agent-slack/internal/render"
)

func messageEvent(channel, user, ts, threadTS string) Event {
	return Event{
		Kind:      EventMessage,
		ChannelID: channel,
		TS:        ts,
		ThreadTS:  threadTS,
		Author:    render.AuthorRef(user, ""),
	}
}

func reactionEvent(channel, user, name, itemTS, eventTS string) Event {
	return Event{
		Kind:      EventReactionAdded,
		ChannelID: channel,
		TS:        itemTS,
		EventTS:   eventTS,
		Author:    render.AuthorRef(user, ""),
		Reaction:  name,
	}
}

func TestFilterDefaultsToMessagesOnly(t *testing.T) {
	f := EventFilter{}
	if !f.Matches(messageEvent("C1", "U2", "1700000010.000100", "")) {
		t.Error("a plain message should match the zero filter")
	}
	if f.Matches(reactionEvent("C1", "U2", "eyes", "1700000010.000100", "1700000020.000100")) {
		t.Error("reactions are opt-in")
	}
	if f.Matches(Event{Kind: EventMessageChanged, ChannelID: "C1", TS: "1700000010.000100"}) {
		t.Error("edits are opt-in")
	}
}

// A channel target means what `message list <channel>` shows; a reply buried in
// someone else's thread is not "a message in this channel".
func TestFilterExcludesThreadRepliesForChannelTargets(t *testing.T) {
	reply := messageEvent("C1", "U2", "1700000020.000200", "1700000010.000100")
	if (EventFilter{Channels: []string{"C1"}}).Matches(reply) {
		t.Error("thread replies should be excluded by default")
	}
	if !(EventFilter{Channels: []string{"C1"}, IncludeThreadReplies: true}).Matches(reply) {
		t.Error("--include-thread-replies should admit them")
	}
}

func TestFilterThreadScopeExcludesTheRootMessage(t *testing.T) {
	f := EventFilter{ThreadTS: "1700000010.000100"}
	root := messageEvent("C1", "U2", "1700000010.000100", "1700000010.000100")
	if f.Matches(root) {
		t.Error("awaiting in a thread means replies, not the message it started from")
	}
	reply := messageEvent("C1", "U2", "1700000020.000200", "1700000010.000100")
	if !f.Matches(reply) {
		t.Error("a reply in the watched thread should match")
	}
	other := messageEvent("C1", "U2", "1700000020.000200", "1700000005.000100")
	if f.Matches(other) {
		t.Error("another thread's reply should not match")
	}
}

// Approval is usually a reaction on the message you posted — the thread root —
// so thread scoping must not exclude it.
func TestFilterThreadScopeAdmitsReactionsOnTheRoot(t *testing.T) {
	f := EventFilter{Kinds: []EventKind{EventReactionAdded}, ThreadTS: "1700000010.000100"}
	if !f.Matches(reactionEvent("C1", "U2", "white_check_mark", "1700000010.000100", "1700000030.000100")) {
		t.Error("a reaction on the thread root should match")
	}
}

func TestFilterExcludesSelfByDefault(t *testing.T) {
	f := EventFilter{SelfUserID: "U_ME"}
	own := messageEvent("C1", "U_ME", "1700000010.000100", "")
	if f.Matches(own) {
		t.Error("your own message is not a reply to yourself")
	}
	f.IncludeSelf = true
	if !f.Matches(own) {
		t.Error("--include-self should admit it")
	}
}

func TestFilterIncludesBotsUnlessExcluded(t *testing.T) {
	bot := Event{Kind: EventMessage, ChannelID: "C1", TS: "1700000010.000100", Author: render.AuthorRef("", "B1")}
	if !(EventFilter{}).Matches(bot) {
		t.Error("app output is what agents usually wait on; bots are included by default")
	}
	if (EventFilter{ExcludeBots: true}).Matches(bot) {
		t.Error("--exclude-bots should drop it")
	}
	if !(EventFilter{From: []string{"B1"}}).Matches(bot) {
		t.Error("--from should match a bot id too")
	}
}

func TestFilterReactionNameIgnoresSkinTone(t *testing.T) {
	f := EventFilter{Kinds: []EventKind{EventReactionAdded}, Reactions: []string{"+1"}}
	if !f.Matches(reactionEvent("C1", "U2", "+1::skin-tone-5", "1700000010.000100", "1700000020.000100")) {
		t.Error("a skin-toned reaction is still that reaction")
	}
	if f.Matches(reactionEvent("C1", "U2", "x", "1700000010.000100", "1700000020.000100")) {
		t.Error("a different reaction should not match")
	}
}

func TestFilterSinceIsExclusive(t *testing.T) {
	f := EventFilter{Since: "1700000010.000100"}
	if f.Matches(messageEvent("C1", "U2", "1700000010.000100", "")) {
		t.Error("--since is exclusive: the cursor's own message must not match")
	}
	if !f.Matches(messageEvent("C1", "U2", "1700000010.000101", "")) {
		t.Error("a later message should match")
	}
}

// InScope is what separates "excluded, report it as skipped" from "someone
// else's Slack" — a rejection in the watched thread must stay visible.
func TestInScopeKeepsExcludedEventsFromTheWatchedThread(t *testing.T) {
	f := EventFilter{
		Kinds:     []EventKind{EventReactionAdded},
		Channels:  []string{"C1"},
		ThreadTS:  "1700000010.000100",
		Reactions: []string{"white_check_mark"},
	}
	rejection := reactionEvent("C1", "U2", "x", "1700000010.000100", "1700000030.000100")
	if f.Matches(rejection) {
		t.Fatal("the narrowing filter should exclude it")
	}
	if !f.InScope(rejection) {
		t.Error("but it happened on the watched message, so it must still be reported")
	}
	elsewhere := reactionEvent("C9", "U2", "x", "1700000010.000100", "1700000030.000100")
	if f.InScope(elsewhere) {
		t.Error("another channel's traffic is not in scope")
	}
}

func TestTSAfterOrdersTimestamps(t *testing.T) {
	if !tsAfter("1700000010.000101", "1700000010.000100") {
		t.Error("micros should order")
	}
	if tsAfter("1700000010.000100", "1700000010.000100") {
		t.Error("equal is not after")
	}
	if !tsAfter("17000000100.000100", "1700000010.000100") {
		t.Error("a longer integer part is later")
	}
}

// Kind is a primary selector: asking for reactions means messages were never
// candidates, so reporting them as "skipped" would bury the real answer.
func TestInScopeIgnoresKindsTheCallerDidNotAskFor(t *testing.T) {
	f := EventFilter{
		Kinds:     []EventKind{EventReactionAdded},
		Channels:  []string{"C1"},
		Reactions: []string{"white_check_mark"},
	}
	if f.InScope(messageEvent("C1", "U2", "1700000010.000100", "")) {
		t.Error("a message is not a skipped reaction")
	}
	if !f.InScope(reactionEvent("C1", "U2", "x", "1700000010.000100", "1700000020.000100")) {
		t.Error("a non-matching reaction is exactly what skipped is for")
	}
}

// The real-world case this exists for: a question posted to a conversation,
// answered by threading on it. A channel watch that drops that reply misses
// exactly what it was started to collect.
func TestFilterAdmitsRepliesToTheAwaitedMessage(t *testing.T) {
	f := EventFilter{Channels: []string{"C1"}, Since: "1700000010.000100", RepliesTo: "1700000010.000100"}
	inThread := messageEvent("C1", "U2", "1700000020.000200", "1700000010.000100")
	if !f.Matches(inThread) {
		t.Error("a reply threaded on the awaited message is an answer")
	}
	inChannel := messageEvent("C1", "U2", "1700000030.000300", "")
	if !f.Matches(inChannel) {
		t.Error("a channel-level reply is also an answer")
	}
	// Other threads stay excluded: RepliesTo is not --include-thread-replies.
	elsewhere := messageEvent("C1", "U2", "1700000040.000400", "1700000005.000100")
	if f.Matches(elsewhere) {
		t.Error("an unrelated thread's reply is not an answer to this message")
	}
}
