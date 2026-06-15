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

// UsergroupsList is a usergroups.list body.
func UsergroupsList(groups ...map[string]any) map[string]any {
	items := make([]any, len(groups))
	for i, g := range groups {
		items[i] = g
	}
	return map[string]any{"ok": true, "usergroups": items}
}

// Usergroup is one usergroups.list subteam object. channels are the group's
// default channels (prefs.channels).
func Usergroup(id, handle, name string, channels ...string) map[string]any {
	chans := make([]any, len(channels))
	for i, c := range channels {
		chans[i] = c
	}
	return map[string]any{
		"id":     id,
		"handle": handle,
		"name":   name,
		"prefs":  map[string]any{"channels": chans, "groups": []any{}},
	}
}

// UsergroupUsers is a usergroups.users.list body.
func UsergroupUsers(userIDs ...string) map[string]any {
	ids := make([]any, len(userIDs))
	for i, u := range userIDs {
		ids[i] = u
	}
	return map[string]any{"ok": true, "users": ids}
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
