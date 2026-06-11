package mockslack

// Canonical Slack response bodies for fixtures. These encode "what Slack
// actually returns" once, so an API-shape learning is one builder edit
// instead of N inline map edits across test packages.

// Message is one conversations.history/replies message object.
func Message(ts, user, text string) map[string]any {
	return map[string]any{"type": "message", "ts": ts, "user": user, "text": text}
}

// History is a conversations.history (or conversations.replies) body.
func History(messages ...map[string]any) map[string]any {
	items := make([]any, len(messages))
	for i, m := range messages {
		items[i] = m
	}
	return map[string]any{"ok": true, "messages": items}
}

// ChannelInfo is a conversations.info body.
func ChannelInfo(id, name string) map[string]any {
	return map[string]any{"ok": true, "channel": map[string]any{"id": id, "name": name}}
}

// UserInfo is a users.info body.
func UserInfo(id, name string) map[string]any {
	return map[string]any{"ok": true, "user": map[string]any{"id": id, "name": name}}
}

// SearchMessages is a search.messages body with single-page paging.
func SearchMessages(matches ...map[string]any) map[string]any {
	items := make([]any, len(matches))
	for i, m := range matches {
		items[i] = m
	}
	return map[string]any{
		"ok": true,
		"messages": map[string]any{
			"matches": items,
			"paging":  map[string]any{"pages": float64(1)},
		},
	}
}

// SearchMatch is one search.messages match.
func SearchMatch(channelID, ts, permalink string) map[string]any {
	return map[string]any{
		"ts":        ts,
		"channel":   map[string]any{"id": channelID},
		"permalink": permalink,
	}
}

// ChannelMatch is a minimal search.messages match carrying only the channel —
// the shape the in:#name channel-resolution trick consumes.
func ChannelMatch(channelID string) map[string]any {
	return map[string]any{"channel": map[string]any{"id": channelID}}
}

// ConversationsList is a conversations.list / users.conversations body.
func ConversationsList(channels ...map[string]any) map[string]any {
	items := make([]any, len(channels))
	for i, ch := range channels {
		items[i] = ch
	}
	return map[string]any{"ok": true, "channels": items}
}

// Channel is one conversations.list channel object.
func Channel(id, name string) map[string]any {
	return map[string]any{"id": id, "name": name}
}
