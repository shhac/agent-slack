package slack

import (
	"context"
	"regexp"
	"strings"

	"github.com/shhac/agent-slack/internal/render"
)

var (
	// A candidate #channel: preceded by start-of-string or a non-word, non-'<'
	// char (so <#C…> tokens and "C#"/"F#" never match), then # + a channel name.
	//
	// This is how a channel reference is told apart from a Markdown heading:
	// Slack has no Markdown headings, and the two shapes don't overlap — a
	// heading is "# " (hash then space, "[^a-z0-9]" right after #, so no match),
	// while a channel is "#name" with a name char flush against the #. Names are
	// lowercase (Slack lowercases them), letters/digits/hyphens/underscores.
	channelMentionCandidateRe = regexp.MustCompile(`(^|[^A-Za-z0-9_<])#([a-z0-9][a-z0-9_-]*)`)

	// All-digit names (issue/PR refs like "#5", "#1234") are never treated as
	// channels — promoting them would mangle prose and waste a lookup per ref.
	allDigitsRe = regexp.MustCompile(`^[0-9]+$`)
)

// ResolveChannelMentions rewrites bare #channel-name tokens into Slack channel
// tokens (<#C…>) so they render as real channel links, mirroring how
// ResolveMentions handles @handle / @group. Already-formed <#…> tokens are
// never matched, names inside code spans/blocks stay literal, and a name that
// doesn't resolve to a channel is left as-is. Best-effort: a resolution error
// leaves the token unchanged.
//
// Resolution is cache-first then a single search.messages lookup — it never
// pays conversations.list's whole-workspace pagination, so an unknown #hashtag
// costs at most one cheap call and stays literal.
func ResolveChannelMentions(ctx context.Context, c *Client, text string) string {
	masked, restore := render.Protect(text, mentionFenceRe, mentionCodeRe)

	matches := channelMentionCandidateRe.FindAllStringSubmatch(masked, -1)
	if len(matches) == 0 {
		return text
	}

	repl := map[string]string{}
	tried := map[string]bool{}
	for _, m := range matches {
		name := strings.ToLower(m[2])
		if tried[name] || allDigitsRe.MatchString(name) {
			continue
		}
		tried[name] = true // a miss is remembered too, so a repeat #name isn't re-looked-up
		if id := resolveChannelIDCheap(ctx, c, name); id != "" {
			repl[name] = "<#" + id + ">"
		}
	}
	if len(repl) == 0 {
		return text
	}

	resolved := channelMentionCandidateRe.ReplaceAllStringFunc(masked, func(match string) string {
		hash := strings.IndexByte(match, '#')
		prefix, name := match[:hash], strings.ToLower(match[hash+1:])
		if token, ok := repl[name]; ok {
			return prefix + token
		}
		return match
	})
	return restore(resolved)
}

// resolveChannelIDCheap resolves a channel name to its id using only the cache
// and the single-call search trick — deliberately NOT the conversations.list
// pagination fallback ResolveChannelID uses, so promoting mentions in a message
// full of #words never triggers a multi-minute workspace scan.
func resolveChannelIDCheap(ctx context.Context, c *Client, name string) string {
	if id, ok := c.cachedChannelID(name); ok {
		return id
	}
	if id := channelIDViaSearch(ctx, c, name); id != "" {
		c.cacheChannelID(name, id)
		return id
	}
	return ""
}
