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
	// Slack issues both U- and W-prefixed user IDs; W belongs to Enterprise
	// Grid and Slack Connect users. They are the same thing everywhere it
	// matters, and treating W as "not a user" does not fail loudly — a
	// W-prefixed target falls through to channel-name resolution, and a
	// W-prefixed reactor is silently dropped from output.
	userIDRe      = regexp.MustCompile(`^[UW][A-Z0-9]{8,}$`)
	botIDRe       = regexp.MustCompile(`^B[A-Z0-9]{8,}$`)
	usergroupIDRe = regexp.MustCompile(`^S[A-Z0-9]{8,}$`)
)

// IsChannelID reports whether s is a Slack conversation ID (channel, DM, or
// group: C…/D…/G…).
func IsChannelID(s string) bool {
	return channelIDRe.MatchString(s)
}

// IsBotID reports whether s is a Slack bot ID. Apps have no user record, so a
// bot id is passed through author filters rather than resolved — which makes
// the shape test load-bearing: a prefix check alone would swallow any handle
// beginning with B and turn it into a filter that never matches.
func IsBotID(s string) bool {
	return botIDRe.MatchString(s)
}

// IsUserID reports whether s is a Slack user ID.
func IsUserID(s string) bool {
	return userIDRe.MatchString(s)
}

// IsUsergroupID reports whether s is a Slack usergroup (subteam) ID: S….
func IsUsergroupID(s string) bool {
	return usergroupIDRe.MatchString(s)
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
