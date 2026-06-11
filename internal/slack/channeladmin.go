package slack

import (
	"context"
	"regexp"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// CreatedChannel is the output of CreateChannel.
type CreatedChannel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsPrivate bool   `json:"is_private"`
}

func CreateChannel(ctx context.Context, c *Client, name string, private bool) (CreatedChannel, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return CreatedChannel{}, agenterrors.New("channel name is empty", agenterrors.FixableByAgent)
	}
	resp, err := c.API(ctx, "conversations.create", map[string]any{
		"name":       name,
		"is_private": private,
	})
	if err != nil {
		return CreatedChannel{}, err
	}
	channel := getRec(resp, "channel")
	id := getStr(channel, "id")
	channelName := getStr(channel, "name")
	if id == "" || channelName == "" {
		return CreatedChannel{}, agenterrors.New("conversations.create returned no channel", agenterrors.FixableByAgent)
	}
	isPrivate := private
	if b, ok := channel["is_private"].(bool); ok {
		isPrivate = b
	}
	return CreatedChannel{ID: id, Name: channelName, IsPrivate: isPrivate}, nil
}

// InviteResult reports per-user invite outcomes.
type InviteResult struct {
	InvitedUserIDs          []string `json:"invited_user_ids"`
	AlreadyInChannelUserIDs []string `json:"already_in_channel_user_ids"`
}

// InviteUsersToChannel invites users one at a time so an already_in_channel
// member doesn't fail the whole batch.
func InviteUsersToChannel(ctx context.Context, c *Client, channelID string, userIDs []string) (InviteResult, error) {
	out := InviteResult{InvitedUserIDs: []string{}, AlreadyInChannelUserIDs: []string{}}
	for _, userID := range userIDs {
		_, err := c.API(ctx, "conversations.invite", map[string]any{
			"channel": channelID,
			"users":   userID,
		})
		if err != nil {
			if ErrorCode(err) == "already_in_channel" {
				out.AlreadyInChannelUserIDs = append(out.AlreadyInChannelUserIDs, userID)
				continue
			}
			return InviteResult{}, err
		}
		out.InvitedUserIDs = append(out.InvitedUserIDs, userID)
	}
	return out, nil
}

// ExternalInviteResult reports per-email Slack Connect invite outcomes.
type ExternalInviteResult struct {
	InvitedEmails        []string `json:"invited_emails"`
	AlreadyInvitedEmails []string `json:"already_invited_emails"`
}

func InviteExternalUsersToChannel(ctx context.Context, c *Client, channelID string, emails []string, externalLimited bool) (ExternalInviteResult, error) {
	out := ExternalInviteResult{InvitedEmails: []string{}, AlreadyInvitedEmails: []string{}}
	for _, email := range emails {
		_, err := c.API(ctx, "conversations.inviteShared", map[string]any{
			"channel":          channelID,
			"emails":           []any{email},
			"external_limited": externalLimited,
		})
		if err != nil {
			code := ErrorCode(err)
			if code == "already_in_channel" || code == "already_invited" {
				out.AlreadyInvitedEmails = append(out.AlreadyInvitedEmails, email)
				continue
			}
			return ExternalInviteResult{}, err
		}
		out.InvitedEmails = append(out.InvitedEmails, email)
	}
	return out, nil
}

// ParseInviteUsersCSV splits and dedupes a --users argument.
func ParseInviteUsersCSV(input string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range strings.Split(input, ",") {
		v := strings.TrimSpace(value)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

var likelyEmailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// SplitEmailsFromInviteTargets separates email targets (external invites)
// from user-ID/handle targets.
func SplitEmailsFromInviteTargets(targets []string) (emails, nonEmails []string) {
	seen := map[string]bool{}
	for _, target := range targets {
		if likelyEmailRe.MatchString(strings.TrimSpace(target)) {
			if !seen[target] {
				seen[target] = true
				emails = append(emails, target)
			}
			continue
		}
		nonEmails = append(nonEmails, target)
	}
	return emails, nonEmails
}
