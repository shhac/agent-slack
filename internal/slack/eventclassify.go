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

// Event is the record both await and stream emit. Field names mirror
// render.CompactMessage so a stream line parses like a message list line.
type Event struct {
	Kind      EventKind             `json:"event"`
	ChannelID string                `json:"channel_id"`
	TS        string                `json:"ts"` // for reactions: the message reacted to
	ThreadTS  string                `json:"thread_ts,omitempty"`
	EventTS   string                `json:"event_ts,omitempty"` // when it happened, when that differs from TS
	Author    *render.CompactAuthor `json:"author,omitempty"`
	Content   string                `json:"content,omitempty"`
	// PreviousContent is the pre-edit body on message_changed.
	PreviousContent string `json:"previous_content,omitempty"`
	Reaction        string `json:"reaction,omitempty"`
	// Message carries the full summary for message-kind events, so the CLI can
	// render files, attachments, and reactions the same way the read commands
	// do. Not serialized: the CLI projects it into the compact shape.
	Message *render.MessageSummary `json:"-"`
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
		return classifyReactionFrame(frame, EventReactionAdded), true
	case "reaction_removed":
		return classifyReactionFrame(frame, EventReactionRemoved), true
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
		return Event{
			Kind:      EventMessage,
			ChannelID: channelID,
			TS:        summary.TS,
			ThreadTS:  summary.ThreadTS,
			Author:    render.AuthorRef(summary.User, summary.BotID),
			Content:   summary.Text,
			Message:   &summary,
		}, true

	case "message_changed":
		edited := getRec(frame, "message")
		previous := getRec(frame, "previous_message")
		summary := SummaryFromRaw(channelID, edited)
		return Event{
			Kind:            EventMessageChanged,
			ChannelID:       channelID,
			TS:              summary.TS,
			ThreadTS:        summary.ThreadTS,
			EventTS:         FirstNonEmpty(getStr(frame, "event_ts"), getStr(frame, "ts")),
			Author:          render.AuthorRef(summary.User, summary.BotID),
			Content:         summary.Text,
			PreviousContent: getStr(previous, "text"),
			Message:         &summary,
		}, true

	case "message_deleted":
		return Event{
			Kind:      EventMessageDeleted,
			ChannelID: channelID,
			TS:        getStr(frame, "deleted_ts"),
			EventTS:   FirstNonEmpty(getStr(frame, "event_ts"), getStr(frame, "ts")),
		}, true

	default:
		// Channel joins/leaves, topic changes, message_replied, and the rest of
		// the subtype zoo: not new activity a caller is waiting on.
		return Event{}, false
	}
}

func classifyReactionFrame(frame map[string]any, kind EventKind) Event {
	item := getRec(frame, "item")
	return Event{
		Kind:      kind,
		ChannelID: getStr(item, "channel"),
		TS:        getStr(item, "ts"),
		EventTS:   FirstNonEmpty(getStr(frame, "event_ts"), getStr(frame, "ts")),
		Author:    render.AuthorRef(getStr(frame, "user"), ""),
		Reaction:  getStr(frame, "reaction"),
	}
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
		Content:   msg.Text,
		Message:   &msg,
	}
}
