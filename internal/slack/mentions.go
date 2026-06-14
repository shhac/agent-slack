package slack

import (
	"context"
	"regexp"
	"strconv"
	"strings"
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
	mentionStashRe = regexp.MustCompile("\x00(\\d+)\x00")
)

// ResolveMentions rewrites bare @handle / @group tokens into Slack mention
// tokens (<@U…> / <!subteam^S…>) so they render as real mentions. User IDs
// (@U…) and broadcasts (@here/@channel/@everyone) are left for the outbound
// formatter, and already-formed <…> tokens are never matched. Unresolved
// handles stay literal. Best-effort: a resolution error leaves the token as-is.
// A name is tried as a user first, then as a usergroup.
func ResolveMentions(ctx context.Context, c *Client, text string) string {
	masked, stash := maskCodeSpans(text)

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
	return unmaskCodeSpans(resolved, stash)
}

func isBroadcastName(lower string) bool {
	return lower == "here" || lower == "channel" || lower == "everyone"
}

// maskCodeSpans replaces fenced and inline code with NUL sentinels so mention
// resolution skips their contents, returning the masked text and the stash to
// restore afterwards.
func maskCodeSpans(text string) (string, []string) {
	var stash []string
	mask := func(re *regexp.Regexp, s string) string {
		return re.ReplaceAllStringFunc(s, func(m string) string {
			stash = append(stash, m)
			return "\x00" + strconv.Itoa(len(stash)-1) + "\x00"
		})
	}
	out := mask(mentionFenceRe, text)
	out = mask(mentionCodeRe, out)
	return out, stash
}

func unmaskCodeSpans(text string, stash []string) string {
	return mentionStashRe.ReplaceAllStringFunc(text, func(m string) string {
		if idx, err := strconv.Atoi(m[1 : len(m)-1]); err == nil && idx < len(stash) {
			return stash[idx]
		}
		return m
	})
}
