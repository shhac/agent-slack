package mockslack

// Fabricated event-socket frames. Shapes follow what a real browser session
// delivers (captured with `agent-slack debug ws-capture`); every id, name, and
// body here is invented. Real captured traffic must never be copied into this
// file — a fixture is a contract about shape, and pasting a live workspace's
// data into the repo leaks it forever.
//
// Socket frames are the bare event, not the Events API's "event_callback"
// envelope: the type is top-level, there is no authorizations block, and —
// unlike the Events API — there is no channel_type field. Conversation kind
// has to be read from the id prefix.
//
// Builders marked "documented only" model a shape Slack documents but that a
// capture has not yet observed; treat their details as less certain than the
// rest.

// Fabricated workspace. Ids are deliberately unlike real Slack ids (which are
// opaque uppercase strings) so a fixture value can never be mistaken for one.
const (
	WSTeamID    = "T_FAKE"
	WSChannelID = "C_FAKE_GENERAL"
	WSDMID      = "D_FAKE_DM"
	WSUserID    = "U_FAKE_ALEX"
	WSOtherUser = "U_FAKE_SAM"
)

// Hello is the first frame after the upgrade. It is far smaller than the
// legacy RTM greeting — no boot payload, because the connect URL sets
// connect_only.
func Hello() map[string]any {
	return map[string]any{
		"type":           "hello",
		"start":          true,
		"fast_reconnect": false,
		"region":         "fake-region",
		"host_id":        "gateway-fake-1",
	}
}

// ReconnectURL is pushed periodically: a pre-authorized URL to reconnect with,
// so a long-lived consumer never has to re-fetch client.getWebSocketURL.
func ReconnectURL() map[string]any {
	return map[string]any{
		"type": "reconnect_url",
		"url":  "wss://fake-gateway.invalid/reconnect/fake-token-placeholder",
	}
}

// WSMessage is a plain message frame. Text arrives twice: as `text` and as
// rich_text `blocks`, which is the authoritative form.
func WSMessage(channel, user, text, ts string) map[string]any {
	return map[string]any{
		"type":                  "message",
		"channel":               channel,
		"user":                  user,
		"text":                  text,
		"blocks":                RichTextBlocks(text),
		"ts":                    ts,
		"event_ts":              ts,
		"team":                  WSTeamID,
		"client_msg_id":         "fake-client-msg-id",
		"source_team":           WSTeamID,
		"user_team":             WSTeamID,
		"suppress_notification": false,
	}
}

// RichTextBlocks is the block payload carried by a plain message.
func RichTextBlocks(text string) []any {
	return []any{map[string]any{
		"type":     "rich_text",
		"block_id": "fake-block-id",
		"elements": []any{map[string]any{
			"type":     "rich_text_section",
			"elements": []any{map[string]any{"type": "text", "text": text}},
		}},
	}}
}

// WSThreadReply is a reply inside a thread. thread_ts is the parent; the reply
// only reaches channel history when it is also broadcast, which is why a
// stream cannot be built on history alone.
func WSThreadReply(channel, user, text, ts, threadTS string) map[string]any {
	frame := WSMessage(channel, user, text, ts)
	frame["thread_ts"] = threadTS
	return frame
}

// WSMessageReplied is the *second* frame a thread reply produces: the parent
// message re-sent with updated reply bookkeeping. Pair every WSThreadReply
// with one — a consumer that treats each message-typed frame as new activity
// will re-emit the parent as though it had just been posted.
func WSMessageReplied(channel, user, parentText, parentTS, replyTS string) map[string]any {
	return map[string]any{
		"type":     "message",
		"subtype":  "message_replied",
		"hidden":   true,
		"channel":  channel,
		"ts":       parentTS,
		"event_ts": replyTS,
		"message": map[string]any{
			"type":              "message",
			"user":              user,
			"text":              parentText,
			"blocks":            RichTextBlocks(parentText),
			"ts":                parentTS,
			"thread_ts":         parentTS,
			"client_msg_id":     "fake-client-msg-id",
			"team":              WSTeamID,
			"is_locked":         false,
			"latest_reply":      replyTS,
			"reply_count":       float64(1),
			"reply_users":       []any{WSOtherUser},
			"reply_users_count": float64(1),
		},
	}
}

// WSBotMessage is a message posted by an app. It has **no `user` field** — the
// author is bot_id/username/bot_profile — so a consumer keyed on `user` drops
// app output entirely, which is most of what an agent waits on.
func WSBotMessage(channel, username, text, ts string) map[string]any {
	return map[string]any{
		"type":                  "message",
		"subtype":               "bot_message",
		"channel":               channel,
		"username":              username,
		"bot_id":                "B_FAKE_APP",
		"app_id":                "A_FAKE_APP",
		"bot_profile":           map[string]any{"id": "B_FAKE_APP", "name": username, "app_id": "A_FAKE_APP", "team_id": WSTeamID},
		"icons":                 map[string]any{"image_48": "https://fake.invalid/icon48.png"},
		"text":                  text,
		"blocks":                RichTextBlocks(text),
		"ts":                    ts,
		"event_ts":              ts,
		"team":                  WSTeamID,
		"source_team":           WSTeamID,
		"user_team":             WSTeamID,
		"suppress_notification": false,
	}
}

// WSMessageChanged is an edit: the payload nests the whole new message and the
// outer ts is the edit's timestamp, not the message's.
func WSMessageChanged(channel, user, text, ts, editTS string) map[string]any {
	return map[string]any{
		"type":     "message",
		"subtype":  "message_changed",
		"hidden":   true,
		"channel":  channel,
		"ts":       editTS,
		"event_ts": editTS,
		"message": map[string]any{
			"type":   "message",
			"user":   user,
			"text":   text,
			"blocks": RichTextBlocks(text),
			"ts":     ts,
			"edited": map[string]any{"user": user, "ts": editTS},
		},
		"previous_message": map[string]any{
			"type": "message",
			"user": user,
			"text": "fabricated text before the edit",
			"ts":   ts,
		},
	}
}

// WSMessageDeleted is a deletion. Like an edit it arrives as a message subtype
// with hidden:true, so a naive "type == message" consumer would surface it as
// a new message with no text. Documented only.
func WSMessageDeleted(channel, deletedTS, eventTS string) map[string]any {
	return map[string]any{
		"type":       "message",
		"subtype":    "message_deleted",
		"hidden":     true,
		"channel":    channel,
		"deleted_ts": deletedTS,
		"ts":         eventTS,
		"event_ts":   eventTS,
	}
}

// WSUserTyping is the typing indicator: by far the highest-volume frame, and
// it carries no message content. thread_ts is set when the typing is inside a
// thread, which is the only signal that a reply is coming to a thread rather
// than the channel.
func WSUserTyping(channel, user, threadTS string) map[string]any {
	frame := map[string]any{
		"type":    "user_typing",
		"id":      float64(1),
		"channel": channel,
		"user":    user,
	}
	if threadTS != "" {
		frame["thread_ts"] = threadTS
	}
	return frame
}

// WSReactionAdded is an emoji reaction on a message.
func WSReactionAdded(channel, user, reaction, itemTS, eventTS string) map[string]any {
	return map[string]any{
		"type":      "reaction_added",
		"user":      user,
		"reaction":  reaction,
		"item_user": WSUserID,
		"item":      map[string]any{"type": "message", "channel": channel, "ts": itemTS},
		"ts":        eventTS,
		"event_ts":  eventTS,
	}
}

// WSReactionRemoved retracts a reaction. Same shape as the add, different
// type — a consumer tracking reaction state that ignores it drifts, and a
// "wait for approval" that ignores it can act on a withdrawn 👍. Documented
// only: no capture has observed one yet.
func WSReactionRemoved(channel, user, reaction, itemTS, eventTS string) map[string]any {
	frame := WSReactionAdded(channel, user, reaction, itemTS, eventTS)
	frame["type"] = "reaction_removed"
	return frame
}

// IMMarked is read-state sync: another of the user's clients marked a DM read.
// A consumer that treats every frame as new activity would report these as
// events the user needs to see.
func IMMarked(channel, ts string) map[string]any {
	return map[string]any{
		"type":                  "im_marked",
		"channel":               channel,
		"ts":                    ts,
		"event_ts":              ts,
		"dm_count":              float64(0),
		"unread_count_display":  float64(0),
		"num_mentions_display":  float64(0),
		"mention_count_display": float64(0),
		"vip_count":             float64(0),
	}
}

// UserInvalidated tells the client its cached copy of a user is stale.
func UserInvalidated(user, eventTS string) map[string]any {
	return map[string]any{"type": "user_invalidated", "user": user, "event_ts": eventTS}
}

// BadgeCountsUpdated is unread-badge bookkeeping, no content.
func BadgeCountsUpdated(eventTS string) map[string]any {
	return map[string]any{
		"type":        "badge_counts_updated",
		"event_ts":    eventTS,
		"activity_v2": map[string]any{"total_unreads": float64(0), "total_mentions": float64(0)},
	}
}

// ClearMentionNotification retracts a mention badge, e.g. once read elsewhere.
func ClearMentionNotification(channel, eventTS string) map[string]any {
	return map[string]any{
		"type":          "clear_mention_notification",
		"channel_id":    channel,
		"team":          WSTeamID,
		"event_ts":      eventTS,
		"clearing_data": map[string]any{"reason": "fabricated-reason"},
	}
}

// ActivityUpdated feeds the client's Activity tab.
func ActivityUpdated(eventTS string) map[string]any {
	return map[string]any{
		"type":     "activity",
		"subtype":  "activity_updated",
		"key":      "fake-activity-key",
		"entry":    map[string]any{"type": "fabricated_activity", "ts": eventTS},
		"event_ts": eventTS,
	}
}

// DesktopNotification is the render-ready notification the client pops. It
// duplicates content already delivered by the message frame, and carries the
// message's ts in `msg` rather than `ts`.
func DesktopNotification(channel, sender, title, content, msgTS, eventTS string) map[string]any {
	return map[string]any{
		"type":              "desktop_notification",
		"channel":           channel,
		"sender_id":         sender,
		"title":             title,
		"subtitle":          "fabricated subtitle",
		"content":           content,
		"msg":               msgTS,
		"ts":                eventTS,
		"event_ts":          eventTS,
		"is_shared":         false,
		"is_channel_invite": false,
		"avatarImage":       "https://fake.invalid/avatar.png",
		"imageUri":          nil,
		"launchUri":         "slack://channel?id=" + channel,
		"ssbFilename":       "fake_notification.mp3",
	}
}

// ThreadSubscribed fires when the user starts following a thread.
func ThreadSubscribed(channel, threadTS, eventTS string) map[string]any {
	return map[string]any{
		"type":     "thread_subscribed",
		"event_ts": eventTS,
		"subscription": map[string]any{
			"type":        "thread",
			"channel":     channel,
			"thread_ts":   threadTS,
			"date_create": float64(1700000000),
			"last_read":   threadTS,
			"active":      true,
		},
	}
}

// UpdateGlobalThreadState is thread unread bookkeeping — no content.
func UpdateGlobalThreadState(eventTS string) map[string]any {
	return map[string]any{
		"type":                     "update_global_thread_state",
		"event_ts":                 eventTS,
		"timestamp":                eventTS,
		"has_unreads":              false,
		"mention_count":            float64(0),
		"vip_count":                float64(0),
		"channel_badges":           map[string]any{},
		"mention_count_by_channel": map[string]any{},
		"unread_count_by_channel":  map[string]any{},
	}
}

// DNDInvalidated tells the client a user's do-not-disturb state is stale.
func DNDInvalidated(user, eventTS string) map[string]any {
	return map[string]any{"type": "dnd_invalidated", "user": user, "event_ts": eventTS}
}

// Pong answers a client ping, echoing its id.
func Pong(id any) map[string]any {
	frame := map[string]any{"type": "pong"}
	if id != nil {
		frame["reply_to"] = id
	}
	return frame
}

// DefaultEventScript is a representative stretch of socket traffic, in roughly
// the proportions a real capture shows: mostly typing and read-state
// bookkeeping, with the occasional message. It exists so a stream/await
// implementation can be built and tested against every frame shape it has to
// survive — including the ones that look like new activity but are not.
//
// Three traps are modelled deliberately, because each one silently corrupts a
// naive consumer: a thread reply arrives as two frames (the reply, then the
// parent again as message_replied); a bot message has no `user`; and edits and
// deletes are message-typed frames with no text of their own.
func DefaultEventScript() []map[string]any {
	return []map[string]any{
		Hello(),
		WSUserTyping(WSChannelID, WSOtherUser, ""),
		WSMessage(WSChannelID, WSOtherUser, "fabricated channel message", "1700000010.000100"),
		WSUserTyping(WSChannelID, WSOtherUser, "1700000010.000100"),
		WSThreadReply(WSChannelID, WSOtherUser, "fabricated thread reply", "1700000020.000200", "1700000010.000100"),
		WSMessageReplied(WSChannelID, WSUserID, "fabricated channel message", "1700000010.000100", "1700000020.000200"),
		ThreadSubscribed(WSChannelID, "1700000010.000100", "1700000020.000300"),
		UpdateGlobalThreadState("1700000020.000400"),
		WSBotMessage(WSChannelID, "Fabricated App", "fabricated app notification", "1700000025.000100"),
		WSMessageChanged(WSChannelID, WSOtherUser, "fabricated text after the edit", "1700000010.000100", "1700000030.000300"),
		WSReactionAdded(WSChannelID, WSOtherUser, "eyes", "1700000010.000100", "1700000040.000400"),
		// A skin-toned reaction: the modifier is part of the name on the wire,
		// so an exact-string match against "+1" misses it.
		WSReactionAdded(WSChannelID, WSOtherUser, "+1::skin-tone-3", "1700000010.000100", "1700000040.000500"),
		WSReactionRemoved(WSChannelID, WSOtherUser, "eyes", "1700000010.000100", "1700000040.000600"),
		WSMessageDeleted(WSChannelID, "1700000020.000200", "1700000050.000500"),
		WSMessage(WSDMID, WSOtherUser, "fabricated direct message", "1700000060.000600"),
		DesktopNotification(WSDMID, WSOtherUser, "Fake Sam", "fabricated direct message", "1700000060.000600", "1700000060.000650"),
		IMMarked(WSDMID, "1700000060.000700"),
		BadgeCountsUpdated("1700000060.000800"),
		ClearMentionNotification(WSDMID, "1700000060.000900"),
		ActivityUpdated("1700000061.000100"),
		UserInvalidated(WSOtherUser, "1700000061.000200"),
		DNDInvalidated(WSOtherUser, "1700000061.000300"),
		ReconnectURL(),
	}
}
