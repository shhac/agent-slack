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

	looksLikeEmail := emailRe.MatchString(trimmed) && !strings.HasPrefix(trimmed, "@")
	if looksLikeEmail {
		if id := userIDViaEmailLookup(ctx, c, trimmed); id != "" {
			return id, nil
		}
	}

	handle := strings.TrimPrefix(trimmed, "@")
	if handle == "" {
		return "", errUserNotResolved(input)
	}
	handleLower := strings.ToLower(handle)
	emailLower := strings.ToLower(trimmed)

	found := ""
	err := EachPage(ctx, c, "users.list", map[string]any{"limit": 200}, func(resp map[string]any) (bool, error) {
		for _, m := range recItems(getArr(resp, "members")) {
			matched := strings.ToLower(getStr(m, "name")) == handleLower
			if !matched && looksLikeEmail {
				email := getStr(getRec(m, "profile"), "email")
				matched = email != "" && strings.ToLower(email) == emailLower
			}
			if !matched {
				continue
			}
			if id := getStr(m, "id"); id != "" {
				found = id
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errUserNotResolved(input)
	}
	return found, nil
}

func userIDViaEmailLookup(ctx context.Context, c *Client, email string) string {
	resp, err := c.API(ctx, "users.lookupByEmail", map[string]any{"email": email})
	if err != nil {
		return "" // fall back to the users.list scan
	}
	return getStr(getRec(resp, "user"), "id")
}

func errUserNotResolved(input string) *agenterrors.APIError {
	return agenterrors.Newf(agenterrors.FixableByAgent, "could not resolve user: %s", strings.TrimSpace(input)).
		WithHint("pass a user ID (U…), @handle, or email — 'agent-slack user list' shows users")
}
