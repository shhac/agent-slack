package render

import (
	"regexp"
	"strings"
)

// MentionResolvers turn entity ids into display labels for inline transcript
// rendering. Each maps an id to a bare label (no leading #/@) or "" when
// unknown — an unresolved token is left as-is, matching how an unresolved
// @user mention stays raw. They are plain funcs so the render layer needs no
// knowledge of the Slack client that supplies the names.
type MentionResolvers struct {
	User      func(id string) string // U…/W… → "Alice"
	Channel   func(id string) string // C…/G… → "general"
	Usergroup func(id string) string // S…    → "eng"
}

func (r MentionResolvers) resolve(f func(string) string, id string) string {
	if f == nil {
		return ""
	}
	return f(id)
}

var (
	// Post-mrkdwn token forms surviving into rendered transcript text. A <…|name>
	// channel/user mention with an embedded label is already collapsed by
	// MrkdwnToMarkdown; what reaches here is the BARE id forms (common from
	// rich_text, which carries only the id) plus tokens mrkdwn never handled
	// (usergroups, slack:// deep links, dates).
	displayChannelTokenRe   = regexp.MustCompile(`<#([CG][A-Z0-9]{7,})(?:\|([^>]*))?>`)
	displayUsergroupTokenRe = regexp.MustCompile(`<!subteam\^(S[A-Z0-9]{7,})(?:\|([^>]*))?>`)
	displaySlackUserRe      = regexp.MustCompile(`<slack://user\?[^>|]*\bid=([UW][A-Z0-9]{7,})[^>|]*(?:\|([^>]*))?>`)
	displaySlackChannelRe   = regexp.MustCompile(`<slack://channel\?[^>|]*\bid=([CG][A-Z0-9]{7,})[^>|]*(?:\|([^>]*))?>`)
	displayDateRe           = regexp.MustCompile(`<!date\^\d+\^[^>|]*(?:\|([^>]*))?>`)
)

// ResolveMentionsForDisplay rewrites the Slack tokens that survive into rendered
// transcript text to human-legible forms, using r where an id lookup is needed.
// It is the read-direction analog of slack.ResolveMentions: code spans and
// fences are masked so a token inside code stays literal. nil resolvers are
// fine — slack:// links and date tokens resolve from their own embedded
// label/fallback and need no lookup, so this still cleans those up.
func ResolveMentionsForDisplay(text string, r MentionResolvers) string {
	if text == "" {
		return text
	}
	masked, restore := Protect(text, mrkdwnFenceRe, mrkdwnCodeRe)

	// Angle-bracket tokens first (unambiguous), then the bare @id user form.
	masked = displaySlackUserRe.ReplaceAllStringFunc(masked, func(m string) string {
		sub := displaySlackUserRe.FindStringSubmatch(m)
		if name := r.resolve(r.User, sub[1]); name != "" {
			return "@" + name
		}
		if sub[2] != "" {
			return atPrefixed(sub[2]) // label is usually "@handle" already
		}
		return "@" + sub[1]
	})
	masked = displaySlackChannelRe.ReplaceAllStringFunc(masked, func(m string) string {
		sub := displaySlackChannelRe.FindStringSubmatch(m)
		if sub[2] != "" {
			return sub[2] // a channel deep-link's label is descriptive (e.g. an incident title)
		}
		if name := r.resolve(r.Channel, sub[1]); name != "" {
			return "#" + name
		}
		return "<#" + sub[1] + ">"
	})
	masked = displayChannelTokenRe.ReplaceAllStringFunc(masked, func(m string) string {
		sub := displayChannelTokenRe.FindStringSubmatch(m)
		if name := r.resolve(r.Channel, sub[1]); name != "" {
			return "#" + name
		}
		if sub[2] != "" {
			return "#" + sub[2]
		}
		return m
	})
	masked = displayUsergroupTokenRe.ReplaceAllStringFunc(masked, func(m string) string {
		sub := displayUsergroupTokenRe.FindStringSubmatch(m)
		if h := r.resolve(r.Usergroup, sub[1]); h != "" {
			return "@" + h
		}
		if sub[2] != "" {
			return atPrefixed(sub[2])
		}
		return m
	})
	masked = displayDateRe.ReplaceAllStringFunc(masked, func(m string) string {
		if sub := displayDateRe.FindStringSubmatch(m); sub[1] != "" {
			return sub[1] // Slack's own pre-formatted fallback string
		}
		return m
	})
	masked = mentionAtIDRe.ReplaceAllStringFunc(masked, func(m string) string {
		id := strings.TrimPrefix(m, "@")
		if name := r.resolve(r.User, id); name != "" {
			return "@" + name
		}
		return m
	})
	return restore(masked)
}

// atPrefixed ensures exactly one leading @ (a usergroup/user label may already
// carry one, e.g. "@paul").
func atPrefixed(s string) string {
	return "@" + strings.TrimPrefix(s, "@")
}

// CollectDisplayIDs gathers the entity ids referenced in ALREADY-RENDERED
// transcript content — the digest path keeps only the rendered string, so its
// raw blocks are gone and CollectReferencedIDs (which walks blocks) can't be
// used. It scans the post-mrkdwn token forms ResolveMentionsForDisplay rewrites,
// so the resolver is built for exactly the ids that will be looked up.
func CollectDisplayIDs(texts ...string) ReferencedIDs {
	var r ReferencedIDs
	seenU, seenC, seenG := map[string]bool{}, map[string]bool{}, map[string]bool{}
	add := func(seen map[string]bool, list *[]string, ok func(string) bool, id string) {
		if ok(id) && !seen[id] {
			seen[id] = true
			*list = append(*list, id)
		}
	}
	for _, t := range texts {
		for _, m := range mentionAtIDRe.FindAllStringSubmatch(t, -1) {
			add(seenU, &r.Users, IsReferencedUserID, m[1])
		}
		for _, m := range displayChannelTokenRe.FindAllStringSubmatch(t, -1) {
			add(seenC, &r.Channels, IsReferencedChannelID, m[1])
		}
		for _, m := range displayUsergroupTokenRe.FindAllStringSubmatch(t, -1) {
			add(seenG, &r.Usergroups, IsReferencedUsergroupID, m[1])
		}
	}
	return r
}
