package render

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// MessageRef identifies a single Slack message extracted from a permalink.
type MessageRef struct {
	WorkspaceURL string
	ChannelID    string
	MessageTS    string // "1234567890.123456"
	ThreadTSHint string // from ?thread_ts=…; used to scan the thread when the message is not in channel history
	Raw          string
	// PossiblyTruncated is set when the URL carries thread_ts but no cid:
	// Slack thread permalinks always include both, so a missing cid usually
	// means an unquoted shell ate everything after the first "&".
	PossiblyTruncated bool
}

var (
	messageIDRe = regexp.MustCompile(`^p(\d{7,})$`)
	messageTSRe = regexp.MustCompile(`^\d{6,}\.\d{6}$`)
)

// IsMessageTS reports whether s is a Slack message timestamp
// ("<seconds>.<microseconds>").
func IsMessageTS(s string) bool {
	return messageTSRe.MatchString(s)
}

// ParseMessageURL parses a Slack permalink
// (https://{workspace}/archives/{channel}/p{ts}[?thread_ts=…&cid=…]).
// The trailing six digits of p<digits> are the microsecond part of the ts.
func ParseMessageURL(input string) (*MessageRef, error) {
	u, err := url.Parse(input)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "invalid URL: %s", input)
	}

	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".slack.com") {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "not a Slack workspace URL: %s", u.Hostname())
	}

	var parts []string
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) < 3 || parts[0] != "archives" {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "unsupported Slack URL path: %s", u.Path)
	}

	channelID := parts[1]
	m := messageIDRe.FindStringSubmatch(parts[2])
	if m == nil {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "unsupported Slack message id: %s", parts[2])
	}
	digits := m[1]
	messageTS := digits[:len(digits)-6] + "." + digits[len(digits)-6:]

	query := u.Query()
	threadTSParam := query.Get("thread_ts")
	threadTSHint := ""
	if messageTSRe.MatchString(threadTSParam) {
		threadTSHint = threadTSParam
	}

	return &MessageRef{
		WorkspaceURL:      u.Scheme + "://" + strings.ToLower(u.Host),
		ChannelID:         channelID,
		MessageTS:         messageTS,
		ThreadTSHint:      threadTSHint,
		Raw:               input,
		PossiblyTruncated: threadTSParam != "" && !query.Has("cid"),
	}, nil
}

// ParseChannelURL recognizes a Slack conversation URL that points at a channel
// rather than a specific message: https://{workspace}/archives/{channelID}
// with no trailing p<ts> message segment. It returns the workspace URL and the
// channel ID. Message permalinks (which carry a third path segment) are not
// matched here — callers try ParseMessageURL first.
func ParseChannelURL(input string) (workspaceURL, channelID string, ok bool) {
	u, err := url.Parse(input)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", false
	}
	if !strings.HasSuffix(strings.ToLower(u.Hostname()), ".slack.com") {
		return "", "", false
	}
	var parts []string
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) != 2 || parts[0] != "archives" || !IsChannelID(parts[1]) {
		return "", "", false
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), parts[1], true
}

// MessageURLParts is the input to BuildMessageURL; ThreadTS is optional.
type MessageURLParts struct {
	WorkspaceURL string
	ChannelID    string
	MessageTS    string
	ThreadTS     string
}

// BuildMessageURL reverses ParseMessageURL. Thread metadata is added only for
// replies (ThreadTS set and different from MessageTS).
func BuildMessageURL(p MessageURLParts) string {
	base := strings.TrimSuffix(p.WorkspaceURL, "/")
	digits := strings.Replace(p.MessageTS, ".", "", 1)
	out := fmt.Sprintf("%s/archives/%s/p%s", base, p.ChannelID, digits)
	if p.ThreadTS != "" && p.ThreadTS != p.MessageTS {
		out += "?thread_ts=" + url.QueryEscape(p.ThreadTS) + "&cid=" + url.QueryEscape(p.ChannelID)
	}
	return out
}
