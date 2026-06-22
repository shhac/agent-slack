package cli

import (
	"context"

	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// transcriptResolvers resolves the users, channels, and usergroups a set of
// referenced ids point at — under the --resolve policy — into the render
// layer's inline display resolvers. It is the transcript (human, inline-rewrite)
// counterpart of resolveReferencedEntities (JSON, referenced_* maps): same
// CollectReferencedIDs → ResolveReferenced machinery and the same `cache warm`
// hint, differing only in that the result rewrites tokens in place rather than
// riding alongside as a map. Returns nil resolvers when resolution is off.
func transcriptResolvers(ctx context.Context, globals *GlobalFlags, cc *clientContext, refs render.ReferencedIDs, mode resolveMode) render.MentionResolvers {
	if !mode.resolve() {
		return render.MentionResolvers{}
	}
	ents := slack.ResolveReferenced(ctx, cc.Client, refs, mode.policy())
	maybeWarmHint(globals, mode, ents.Fetched)
	return displayResolvers(ents)
}

// displayResolvers adapts resolved entities to id→label lookups: a user's
// display label, a channel's name, a usergroup's handle (then name). Each
// returns "" for an id that didn't resolve, so the token is left as-is.
func displayResolvers(ents slack.ReferencedEntities) render.MentionResolvers {
	return render.MentionResolvers{
		User: func(id string) string {
			if u, ok := ents.Users[id]; ok {
				return u.DisplayLabel()
			}
			return ""
		},
		Channel: func(id string) string {
			if c, ok := ents.Channels[id]; ok {
				return c.Name
			}
			return ""
		},
		Usergroup: func(id string) string {
			if g, ok := ents.Usergroups[id]; ok {
				if g.Handle != "" {
					return g.Handle
				}
				return g.Name
			}
			return ""
		},
	}
}
