package slack

import (
	"context"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// OpenDMChannel opens (or reuses) a DM with one user and returns its ID. The
// user→channel mapping is permanent, so a cache hit skips conversations.open
// entirely; the message history that follows is still fetched live.
func OpenDMChannel(ctx context.Context, c *Client, userID string) (string, error) {
	members := []string{userID}
	if channelID, ok := c.cachedDMChannel(members); ok {
		return channelID, nil
	}
	resp, err := c.API(ctx, "conversations.open", map[string]any{"users": userID})
	if err != nil {
		return "", err
	}
	channelID := getStr(getRec(resp, "channel"), "id")
	if channelID == "" {
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "could not open DM channel for user: %s", userID)
	}
	c.cacheDMChannel(members, channelID)
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

	channelID, ok := c.cachedDMChannel(userIDs)
	if !ok {
		resp, err := c.API(ctx, "conversations.open", map[string]any{"users": strings.Join(userIDs, ",")})
		if err != nil {
			return DMOpenResult{}, err
		}
		channelID = getStr(getRec(resp, "channel"), "id")
		if channelID == "" {
			return DMOpenResult{}, agenterrors.New("conversations.open returned no channel", agenterrors.FixableByAgent)
		}
		c.cacheDMChannel(userIDs, channelID)
	}
	channelType := "group_dm"
	if strings.HasPrefix(channelID, "D") {
		channelType = "dm"
	}
	return DMOpenResult{UserIDs: userIDs, DMChannelID: channelID, ChannelType: channelType}, nil
}
