package slack

import (
	"context"
	"regexp"
	"strings"

	"github.com/shhac/agent-slack/internal/render"
)

var (
	// A candidate @handle: preceded by start-of-string or a non-word, non-'<'
	// char (so <@U…> tokens and emails like a@b never match), then @ + handle.
	mentionCandidateRe = regexp.MustCompile(`(^|[^A-Za-z0-9_<])@([A-Za-z][A-Za-z0-9._-]*)`)
	bareUserIDRe       = regexp.MustCompile(`^[UWB][A-Z0-9]{6,}$`)

	// Code spans / fenced blocks are masked during resolution so an @handle
	// inside code stays literal.
	mentionFenceRe = regexp.MustCompile("(?s)```.*?```")
	mentionCodeRe  = regexp.MustCompile("`[^`\n]+`")
)

// ResolveMentions rewrites bare @handle / @group tokens into Slack mention
// tokens (<@U…> / <!subteam^S…>) so they render as real mentions. User IDs
// (@U…) and broadcasts (@here/@channel/@everyone) are left for the outbound
// formatter, and already-formed <…> tokens are never matched. Unresolved
// handles stay literal. Best-effort: a resolution error leaves the token as-is.
// A name is tried as a user first, then as a usergroup.
func ResolveMentions(ctx context.Context, c *Client, text string) string {
	masked, restore := render.Protect(text, mentionFenceRe, mentionCodeRe)

	matches := mentionCandidateRe.FindAllStringSubmatch(masked, -1)
	if len(matches) == 0 {
		return text
	}

	repl := map[string]string{}
	for _, m := range matches {
		handle := m[2]
		key := strings.ToLower(handle)
		if _, done := repl[key]; done {
			continue
		}
		if bareUserIDRe.MatchString(handle) || isBroadcastName(key) {
			continue
		}
		if id, err := ResolveUserID(ctx, c, handle); err == nil && id != "" {
			repl[key] = "<@" + id + ">"
			continue
		}
		if id, err := ResolveUsergroupID(ctx, c, handle); err == nil && id != "" {
			repl[key] = "<!subteam^" + id + ">"
		}
	}
	if len(repl) == 0 {
		return text
	}

	resolved := mentionCandidateRe.ReplaceAllStringFunc(masked, func(match string) string {
		at := strings.IndexByte(match, '@')
		prefix, handle := match[:at], match[at+1:]
		if token, ok := repl[strings.ToLower(handle)]; ok {
			return prefix + token
		}
		return match
	})
	return restore(resolved)
}

func isBroadcastName(lower string) bool {
	return lower == "here" || lower == "channel" || lower == "everyone"
}
