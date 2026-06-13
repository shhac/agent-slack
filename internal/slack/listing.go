package slack

import "context"

const defaultConversationTypes = "public_channel,private_channel,im,mpim"

// CompactChannel is the token-lean conversation projection for channel list.
type CompactChannel struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	IsPrivate  bool   `json:"is_private,omitempty"`
	IsIM       bool   `json:"is_im,omitempty"`
	IsMpim     bool   `json:"is_mpim,omitempty"`
	IsArchived bool   `json:"is_archived,omitempty"`
	IsMember   bool   `json:"is_member,omitempty"`
	User       string `json:"user,omitempty"` // DM counterpart
	NumMembers int    `json:"num_members,omitempty"`
	Topic      string `json:"topic,omitempty"`
}

// ToCompactChannel shapes one raw conversations.list channel object.
func ToCompactChannel(ch map[string]any) CompactChannel {
	return CompactChannel{
		ID:         getStr(ch, "id"),
		Name:       getStr(ch, "name"),
		IsPrivate:  getBool(ch, "is_private"),
		IsIM:       getBool(ch, "is_im"),
		IsMpim:     getBool(ch, "is_mpim"),
		IsArchived: getBool(ch, "is_archived"),
		IsMember:   getBool(ch, "is_member"),
		User:       getStr(ch, "user"),
		NumMembers: int(getNum(ch, "num_members")),
		Topic:      getStr(getRec(ch, "topic"), "value"),
	}
}

// ConversationsOptions controls ListConversations.
type ConversationsOptions struct {
	// All lists every workspace conversation (conversations.list); otherwise
	// users.conversations lists memberships — User's, or the authed user's
	// when User is empty.
	All    bool
	User   string
	Limit  int // default 100, clamped to [1, 1000]
	Cursor string
	Types  string // default public_channel,private_channel,im,mpim
}

// ConversationsPage is one page of raw channel objects plus the next cursor.
type ConversationsPage struct {
	Channels   []map[string]any
	NextCursor string
}

// ListConversations returns one page of conversations (all, or a user's).
func ListConversations(ctx context.Context, c *Client, opts ConversationsOptions) (ConversationsPage, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = 100
	}
	types := opts.Types
	if types == "" {
		types = defaultConversationTypes
	}
	pages := c.conversationsPageCache()
	pageKey := conversationsPageKey(opts)
	if page, ok := pages.get(pageKey); ok {
		return page, nil
	}

	method := "users.conversations"
	if opts.All {
		method = "conversations.list"
	}
	params := map[string]any{
		"limit":            clampInt(limit, 1, 1000),
		"types":            types,
		"exclude_archived": true,
	}
	if opts.Cursor != "" {
		params["cursor"] = opts.Cursor
	}
	if !opts.All && opts.User != "" {
		params["user"] = opts.User
	}
	resp, err := c.API(ctx, method, params)
	if err != nil {
		return ConversationsPage{}, err
	}
	page := ConversationsPage{
		Channels:   recItems(getArr(resp, "channels")),
		NextCursor: NextCursor(resp),
	}

	warm := make([]CompactChannel, 0, len(page.Channels))
	for _, ch := range page.Channels {
		warm = append(warm, ToCompactChannel(ch))
	}
	c.warmChannelCache(warm)

	pages.set(pageKey, page)
	pages.save()
	return page, nil
}
