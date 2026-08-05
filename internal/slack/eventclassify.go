package slack

// Turning raw socket frames into the events a caller asked for. The socket is
// a firehose of the whole workspace — roughly fifteen bookkeeping frames per
// real message — so classification is an allowlist: a frame becomes an event
// only if it is one of the kinds below, and everything else is dropped.
//
// The frame shapes this guards against are documented in
// design-docs/behavior-reference.md and modelled by mockslack's fixture.

import (
	"github.com/shhac/agent-slack/internal/render"
)

// EventKind is the discriminator on an emitted event.
type EventKind string

const (
	EventMessage         EventKind = "message"
	EventReactionAdded   EventKind = "reaction_added"
	EventReactionRemoved EventKind = "reaction_removed"
	EventMessageChanged  EventKind = "message_changed"
	EventMessageDeleted  EventKind = "message_deleted"
)

// Event is the classified record the delivery engine works in. It is NOT the
// output shape — the CLI projects it (see compactEvent) so bodies can be
// truncated and referenced entities merged in — so it carries no json tags: a
// tagged field here would look like it ships and never appear in output.
type Event struct {
	Kind      EventKind
	ChannelID string
	// TS is the message this event concerns: for a reaction, the message
	// reacted to; for an edit or delete, the message changed.
	TS string
	// ThreadTS is the parent of a threaded message. Reactions do not carry it —
	// they are scoped by the message they target.
	ThreadTS string
	// EventTS is when the event happened, which differs from TS for anything
	// that acts on an existing message.
	EventTS string
	Author  *render.CompactAuthor
	// PreviousContent is the pre-edit body on message_changed.
	PreviousContent string
	Reaction        string
	// Message is the full summary for message-kind events, and the single
	// source of the body: a separate Content field would duplicate Message.Text
	// and drift from it.
	Message *render.MessageSummary
}

// Content is the event's body, read through Message so there is one copy.
func (e Event) Content() string {
	if e.Message == nil {
		return ""
	}
	return e.Message.Text
}

// Cursor is the timestamp a resumed run should continue strictly after: when
// an event happened, which is not always the ts it points at.
func (e Event) Cursor() string {
	if e.EventTS != "" {
		return e.EventTS
	}
	return e.TS
}

// IsBot reports an event authored by an app rather than a person.
func (e Event) IsBot() bool { return e.Author != nil && e.Author.BotID != "" }

// AuthorID is the user or bot id behind the event, whichever is set.
func (e Event) AuthorID() string {
	if e.Author == nil {
		return ""
	}
	if e.Author.UserID != "" {
		return e.Author.UserID
	}
	return e.Author.BotID
}

// ClassifyFrame maps one raw socket frame to an Event. ok is false for every
// frame that is not new activity — the large majority.
func ClassifyFrame(frame map[string]any) (Event, bool) {
	switch getStr(frame, "type") {
	case "message":
		return classifyMessageFrame(frame)
	case "reaction_added":
		return classifyReactionFrame(frame, EventReactionAdded)
	case "reaction_removed":
		return classifyReactionFrame(frame, EventReactionRemoved)
	default:
		return Event{}, false
	}
}

// classifyMessageFrame splits the message-typed frames by subtype. Edits and
// deletes arrive here too, hidden and textless; message_replied is the parent
// re-sent after a reply and is always dropped, since forwarding it re-emits an
// old message as though it were new.
func classifyMessageFrame(frame map[string]any) (Event, bool) {
	channelID := getStr(frame, "channel")
	switch getStr(frame, "subtype") {
	case "", "bot_message", "thread_broadcast", "file_share", "me_message":
		summary := SummaryFromRaw(channelID, frame)
		if channelID == "" || summary.TS == "" {
			// Without a conversation and a timestamp an event cannot be
			// filtered, deduped, or resumed from — and a workspace-wide stream
			// would carry it as a message with no identity at all.
			return Event{}, false
		}
		return Event{
			Kind:      EventMessage,
			ChannelID: channelID,
			TS:        summary.TS,
			ThreadTS:  summary.ThreadTS,
			Author:    render.AuthorRef(summary.User, summary.BotID),
			Message:   &summary,
		}, true

	case "message_changed":
		edited := getRec(frame, "message")
		previous := getRec(frame, "previous_message")
		summary := SummaryFromRaw(channelID, edited)
		if channelID == "" || summary.TS == "" {
			return Event{}, false
		}
		return Event{
			Kind:            EventMessageChanged,
			ChannelID:       channelID,
			TS:              summary.TS,
			ThreadTS:        summary.ThreadTS,
			EventTS:         FirstNonEmpty(getStr(frame, "event_ts"), getStr(frame, "ts")),
			Author:          render.AuthorRef(summary.User, summary.BotID),
			PreviousContent: getStr(previous, "text"),
			Message:         &summary,
		}, true

	case "message_deleted":
		deletedTS := getStr(frame, "deleted_ts")
		if channelID == "" || deletedTS == "" {
			return Event{}, false
		}
		// The deleted body is gone, but Slack still names who wrote it in
		// previous_message — without which --from, --exclude-bots, and
		// self-exclusion cannot filter a delete at all.
		previous := getRec(frame, "previous_message")
		return Event{
			Kind:      EventMessageDeleted,
			ChannelID: channelID,
			TS:        deletedTS,
			EventTS:   FirstNonEmpty(getStr(frame, "event_ts"), getStr(frame, "ts")),
			Author:    render.AuthorRef(getStr(previous, "user"), getStr(previous, "bot_id")),
		}, true

	default:
		// Channel joins/leaves, topic changes, message_replied, and the rest of
		// the subtype zoo: not new activity a caller is waiting on.
		return Event{}, false
	}
}

// classifyReactionFrame keeps only reactions on messages. Slack also reports
// reactions on files, which carry no conversation or message timestamp — as
// events they would be indistinguishable from a malformed frame.
func classifyReactionFrame(frame map[string]any, kind EventKind) (Event, bool) {
	item := getRec(frame, "item")
	if getStr(item, "type") != "message" || getStr(item, "channel") == "" || getStr(item, "ts") == "" {
		return Event{}, false
	}
	return Event{
		Kind:      kind,
		ChannelID: getStr(item, "channel"),
		TS:        getStr(item, "ts"),
		EventTS:   FirstNonEmpty(getStr(frame, "event_ts"), getStr(frame, "ts")),
		Author:    render.AuthorRef(getStr(frame, "user"), ""),
		Reaction:  getStr(frame, "reaction"),
	}, true
}

// EventFromMessage adapts a history/thread message into a message event, so a
// backfill and the live socket produce the same records.
func EventFromMessage(channelID string, msg render.MessageSummary) Event {
	return Event{
		Kind:      EventMessage,
		ChannelID: channelID,
		TS:        msg.TS,
		ThreadTS:  msg.ThreadTS,
		Author:    render.AuthorRef(msg.User, msg.BotID),
		Message:   &msg,
	}
}
