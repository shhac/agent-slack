package slack

import (
	"context"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

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
	return ConversationsPage{
		Channels:   recItems(getArr(resp, "channels")),
		NextCursor: NextCursor(resp),
	}, nil
}

// ListUsersOptions controls ListUsers.
type ListUsersOptions struct {
	Limit       int // default 200, clamped to [1, 1000]
	Cursor      string
	IncludeBots bool
}

// UsersPage is a page of compact users plus the next cursor.
type UsersPage struct {
	Users      []CompactUser
	NextCursor string
}

// ListUsers pages users.list until limit users accumulate, then annotates
// each with their open DM channel id (one conversations.list types=im sweep).
func ListUsers(ctx context.Context, c *Client, opts ListUsersOptions) (UsersPage, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = 200
	}
	limit = clampInt(limit, 1, 1000)

	var users []CompactUser
	nextCursor := ""
	cursor := opts.Cursor
	for len(users) < limit {
		pageSize := min(200, limit-len(users))
		pageParams := map[string]any{"limit": pageSize}
		if cursor != "" {
			pageParams["cursor"] = cursor
		}
		resp, err := c.API(ctx, "users.list", pageParams)
		if err != nil {
			return UsersPage{}, err
		}
		for _, m := range recItems(getArr(resp, "members")) {
			if getStr(m, "id") == "" {
				continue
			}
			if !opts.IncludeBots && getBool(m, "is_bot") {
				continue
			}
			users = append(users, ToCompactUser(m))
			if len(users) >= limit {
				break
			}
		}
		next := NextCursor(resp)
		if next == "" {
			nextCursor = ""
			break
		}
		cursor = next
		nextCursor = next
	}

	dmMap, err := fetchDMMap(ctx, c)
	if err != nil {
		return UsersPage{}, err
	}
	for i := range users {
		users[i].DMID = dmMap[users[i].ID]
	}
	return UsersPage{Users: users, NextCursor: nextCursor}, nil
}

func fetchDMMap(ctx context.Context, c *Client) (map[string]string, error) {
	out := map[string]string{}
	err := EachPage(ctx, c, "conversations.list", map[string]any{"types": "im", "limit": 200}, func(resp map[string]any) (bool, error) {
		for _, ch := range recItems(getArr(resp, "channels")) {
			id := getStr(ch, "id")
			user := getStr(ch, "user")
			if id != "" && user != "" {
				out[user] = id
			}
		}
		return true, nil
	})
	return out, err
}

// GetUser fetches one user by ID, @handle, or email.
func GetUser(ctx context.Context, c *Client, input string) (CompactUser, error) {
	userID, err := ResolveUserID(ctx, c, input)
	if err != nil {
		return CompactUser{}, err
	}
	resp, err := c.API(ctx, "users.info", map[string]any{"user": userID})
	if err != nil {
		return CompactUser{}, err
	}
	user := getRec(resp, "user")
	if getStr(user, "id") == "" {
		return CompactUser{}, agenterrors.New("users.info returned no user", agenterrors.FixableByAgent)
	}
	return ToCompactUser(user), nil
}

// OpenDMChannel opens (or reuses) a DM with one user and returns its ID.
func OpenDMChannel(ctx context.Context, c *Client, userID string) (string, error) {
	resp, err := c.API(ctx, "conversations.open", map[string]any{"users": userID})
	if err != nil {
		return "", err
	}
	channelID := getStr(getRec(resp, "channel"), "id")
	if channelID == "" {
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "could not open DM channel for user: %s", userID)
	}
	return channelID, nil
}

// DMOpenResult is the output of GetDMChannelForUsers.
type DMOpenResult struct {
	UserIDs     []string `json:"user_ids"`
	DMChannelID string   `json:"dm_channel_id"`
	ChannelType string   `json:"channel_type"` // "dm" | "group_dm"
}

// GetDMChannelForUsers opens a DM or group DM (max 8 users) and returns the
// channel info.
func GetDMChannelForUsers(ctx context.Context, c *Client, inputs []string) (DMOpenResult, error) {
	if len(inputs) == 0 {
		return DMOpenResult{}, agenterrors.New("at least one user is required", agenterrors.FixableByAgent)
	}
	if len(inputs) > 8 {
		return DMOpenResult{}, agenterrors.New("Slack supports a maximum of 8 users in a group DM", agenterrors.FixableByAgent)
	}

	var userIDs []string
	for _, input := range inputs {
		if strings.TrimSpace(input) == "" {
			continue
		}
		id, err := ResolveUserID(ctx, c, input)
		if err != nil {
			return DMOpenResult{}, err
		}
		userIDs = append(userIDs, id)
	}
	if len(userIDs) == 0 {
		return DMOpenResult{}, agenterrors.New("no valid users provided", agenterrors.FixableByAgent)
	}

	resp, err := c.API(ctx, "conversations.open", map[string]any{"users": strings.Join(userIDs, ",")})
	if err != nil {
		return DMOpenResult{}, err
	}
	channelID := getStr(getRec(resp, "channel"), "id")
	if channelID == "" {
		return DMOpenResult{}, agenterrors.New("conversations.open returned no channel", agenterrors.FixableByAgent)
	}
	channelType := "group_dm"
	if strings.HasPrefix(channelID, "D") {
		channelType = "dm"
	}
	return DMOpenResult{UserIDs: userIDs, DMChannelID: channelID, ChannelType: channelType}, nil
}

// MarkConversation marks a channel read up to ts.
func MarkConversation(ctx context.Context, c *Client, channelID, ts string) error {
	_, err := c.API(ctx, "conversations.mark", map[string]any{"channel": channelID, "ts": ts})
	return err
}
