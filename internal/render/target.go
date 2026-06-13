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
// ("#name" or ID), or a user (DM target). Name/handle→ID resolution needs the
// API and happens in the client layer.
type Target struct {
	Kind    TargetKind
	Ref     *MessageRef // Kind == TargetURL
	Channel string      // Kind == TargetChannel: "#name" or a C…/D…/G… ID
	UserID  string      // Kind == TargetUser: a U… id or an unresolved "@handle"
	// WorkspaceURL is set when a TargetChannel was given as a channel URL
	// (https://team.slack.com/archives/C…); it pins the workspace the same way
	// a permalink does. Empty for bare names/IDs, which use the default.
	WorkspaceURL string
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

// ParseTarget interprets a CLI <target> argument. A U… id or an "@handle" is a
// user (DM) target; a permalink, channel URL, #name, or C…/G…/D… id is a
// channel; anything else is a bare channel name normalized to "#name".
func ParseTarget(input string) (Target, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return Target{}, agenterrors.New("missing target", agenterrors.FixableByAgent).
			WithHint("pass a Slack permalink, #channel, channel ID, @handle, or user ID")
	}

	if ref, err := ParseMessageURL(trimmed); err == nil {
		return Target{Kind: TargetURL, Ref: ref}, nil
	}
	if wsURL, channelID, ok := ParseChannelURL(trimmed); ok {
		return Target{Kind: TargetChannel, Channel: channelID, WorkspaceURL: wsURL}, nil
	}

	if IsUserID(trimmed) {
		return Target{Kind: TargetUser, UserID: trimmed}, nil
	}
	// "@handle" (or "@U…") is a user target; the handle resolves to an id in
	// the client layer.
	if rest, ok := strings.CutPrefix(trimmed, "@"); ok && rest != "" {
		if IsUserID(rest) {
			return Target{Kind: TargetUser, UserID: rest}, nil
		}
		return Target{Kind: TargetUser, UserID: trimmed}, nil
	}
	if strings.HasPrefix(trimmed, "#") || IsChannelID(trimmed) {
		return Target{Kind: TargetChannel, Channel: trimmed}, nil
	}

	// Bare channel names ("general") are allowed for convenience.
	return Target{Kind: TargetChannel, Channel: "#" + trimmed}, nil
}
