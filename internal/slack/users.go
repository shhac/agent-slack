package slack

import (
	"context"
	"regexp"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// CompactUser is the token-lean user projection emitted by user commands and
// --resolve-users expansions.
type CompactUser struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"` // handle
	RealName    string `json:"real_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Title       string `json:"title,omitempty"`
	TZ          string `json:"tz,omitempty"`
	IsBot       bool   `json:"is_bot,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`
	DMID        string `json:"dm_id,omitempty"`
}

// DisplayLabel is the name to show for a user: display name, then real name,
// then the bare handle. The canonical speaker/mention precedence shared by the
// transcript and digest renderers. ("" only when the user has no name at all.)
func (u CompactUser) DisplayLabel() string {
	return FirstNonEmpty(u.DisplayName, u.RealName, u.Name)
}

// ToCompactUser shapes a raw users.list / users.info member object.
func ToCompactUser(u map[string]any) CompactUser {
	profile := getRec(u, "profile")
	realName := getStr(u, "real_name")
	if realName == "" {
		realName = getStr(profile, "real_name")
	}
	return CompactUser{
		ID:          getStr(u, "id"),
		Name:        getStr(u, "name"),
		RealName:    realName,
		DisplayName: getStr(profile, "display_name"),
		Email:       getStr(profile, "email"),
		Title:       getStr(profile, "title"),
		TZ:          getStr(u, "tz"),
		IsBot:       getBool(u, "is_bot"),
		Deleted:     getBool(u, "deleted"),
	}
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ResolveUserID turns "U…", "@handle", "handle", or an email address into a
// user ID. Emails try users.lookupByEmail first; handles (and emails when the
// lookup is unavailable) scan users.list pages.
func ResolveUserID(ctx context.Context, c *Client, input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if render.IsUserID(trimmed) {
		return trimmed, nil
	}

	cacheKey := handleCacheKey(trimmed)
	if id, ok := c.cachedUserIDByHandle(cacheKey); ok {
		return id, nil
	}

	looksLikeEmail := emailRe.MatchString(trimmed) && !strings.HasPrefix(trimmed, "@")
	if looksLikeEmail {
		if id := userIDViaEmailLookup(ctx, c, trimmed); id != "" {
			c.cacheUserIDByHandle(cacheKey, id)
			return id, nil
		}
	}

	handle := strings.TrimPrefix(trimmed, "@")
	if handle == "" {
		return "", errUserNotResolved(input)
	}
	if c.usersComplete() {
		return "", errUserNotResolved(input) // authoritative: complete set lacks this handle/email
	}

	// Scan users.list to completion. On a match we early-exit; on a full miss
	// we've enumerated everyone, so warm the whole set (handles + entity, bots
	// included) and mark it complete — turning later misses into cache hits.
	var all []CompactUser
	found := ""
	err := EachPage(ctx, c, "users.list", map[string]any{"limit": 200}, func(resp map[string]any) (bool, error) {
		for _, m := range recItems(getArr(resp, "members")) {
			cu := ToCompactUser(m)
			all = append(all, cu)
			matched := strings.EqualFold(cu.Name, handle)
			if !matched && looksLikeEmail && cu.Email != "" {
				matched = strings.EqualFold(cu.Email, trimmed)
			}
			if matched && cu.ID != "" {
				found = cu.ID
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if found != "" {
		c.cacheUserIDByHandle(cacheKey, found)
		return found, nil
	}
	c.warmUserCache(all, true) // full miss → the set is complete
	return "", errUserNotResolved(input)
}

func userIDViaEmailLookup(ctx context.Context, c *Client, email string) string {
	resp, err := c.API(ctx, "users.lookupByEmail", map[string]any{"email": email})
	if err != nil {
		return "" // fall back to the users.list scan
	}
	return getStr(getRec(resp, "user"), "id")
}

func errUserNotResolved(input string) *agenterrors.APIError {
	return errResolveFailed("user: "+strings.TrimSpace(input),
		"pass a user ID (U…), @handle, or email — 'agent-slack user list' shows users")
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

	pages := c.usersPageCache()
	pageKey := usersPageKey(opts)
	if page, ok := pages.get(pageKey); ok {
		return page, nil
	}

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
	c.warmUserCache(users, false) // a bounded list page is not a full enumeration

	page := UsersPage{Users: users, NextCursor: nextCursor}
	pages.set(pageKey, page)
	pages.save()
	return page, nil
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
	// A profile cached within the short Get window is complete (users.list and
	// users.info return the same fields), so serve it without users.info.
	serve := c.usersCache()
	if u, ok := serve.getWithin(userID, cacheTTLOf(c.cache).Get); ok {
		return u, nil
	}
	resp, err := c.API(ctx, "users.info", map[string]any{"user": userID})
	if err != nil {
		return CompactUser{}, err
	}
	user := getRec(resp, "user")
	if getStr(user, "id") == "" {
		return CompactUser{}, agenterrors.New("users.info returned no user", agenterrors.FixableByAgent)
	}
	compact := ToCompactUser(user)
	c.warmUserCache([]CompactUser{compact}, false) // grow/refresh the cache from a direct get
	return compact, nil
}
