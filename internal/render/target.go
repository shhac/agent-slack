package render

import (
	"regexp"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// TargetKind discriminates what a CLI <target> argument resolved to.
type TargetKind string

const (
	TargetURL     TargetKind = "url"
	TargetChannel TargetKind = "channel"
	TargetUser    TargetKind = "user"
)

// Target is a parsed CLI <target>: a message permalink, a channel
// ("#name" or ID), or a user ID (DM target). Name→ID resolution needs the
// API and happens in the client layer.
type Target struct {
	Kind    TargetKind
	Ref     *MessageRef // Kind == TargetURL
	Channel string      // Kind == TargetChannel: "#name" or a C…/D…/G… ID
	UserID  string      // Kind == TargetUser
}

var (
	channelIDRe = regexp.MustCompile(`^[CDG][A-Z0-9]{8,}$`)
	userIDRe    = regexp.MustCompile(`^U[A-Z0-9]{8,}$`)
)

// IsChannelID reports whether s is a Slack conversation ID (channel, DM, or
// group: C…/D…/G…).
func IsChannelID(s string) bool {
	return channelIDRe.MatchString(s)
}

// IsUserID reports whether s is a Slack user ID.
func IsUserID(s string) bool {
	return userIDRe.MatchString(s)
}

// ParseTarget interprets a CLI <target> argument. Anything that is not a
// permalink, user ID, or channel ID is treated as a bare channel name and
// normalized to "#name".
func ParseTarget(input string) (Target, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return Target{}, agenterrors.New("missing target", agenterrors.FixableByAgent).
			WithHint("pass a Slack permalink, #channel, channel ID, or user ID")
	}

	if ref, err := ParseMessageURL(trimmed); err == nil {
		return Target{Kind: TargetURL, Ref: ref}, nil
	}

	if IsUserID(trimmed) {
		return Target{Kind: TargetUser, UserID: trimmed}, nil
	}
	if strings.HasPrefix(trimmed, "#") || IsChannelID(trimmed) {
		return Target{Kind: TargetChannel, Channel: trimmed}, nil
	}

	// Bare channel names ("general") are allowed for convenience.
	return Target{Kind: TargetChannel, Channel: "#" + trimmed}, nil
}
