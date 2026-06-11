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
	profile, _ := u["profile"].(map[string]any)
	str := func(m map[string]any, key string) string {
		s, _ := m[key].(string)
		return s
	}
	boolVal := func(key string) bool {
		b, _ := u[key].(bool)
		return b
	}
	realName := str(u, "real_name")
	if realName == "" {
		realName = str(profile, "real_name")
	}
	return CompactUser{
		ID:          str(u, "id"),
		Name:        str(u, "name"),
		RealName:    realName,
		DisplayName: str(profile, "display_name"),
		Email:       str(profile, "email"),
		Title:       str(profile, "title"),
		TZ:          str(u, "tz"),
		IsBot:       boolVal("is_bot"),
		Deleted:     boolVal("deleted"),
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
		members, _ := resp["members"].([]any)
		for _, mAny := range members {
			m, ok := mAny.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			matched := strings.ToLower(name) == handleLower
			if !matched && looksLikeEmail {
				profile, _ := m["profile"].(map[string]any)
				email, _ := profile["email"].(string)
				matched = email != "" && strings.ToLower(email) == emailLower
			}
			if !matched {
				continue
			}
			if id, _ := m["id"].(string); id != "" {
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
	user, _ := resp["user"].(map[string]any)
	id, _ := user["id"].(string)
	return id
}

func errUserNotResolved(input string) *agenterrors.APIError {
	return agenterrors.Newf(agenterrors.FixableByAgent, "could not resolve user: %s", strings.TrimSpace(input)).
		WithHint("pass a user ID (U…), @handle, or email — 'agent-slack user list' shows users")
}
