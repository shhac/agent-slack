package render

import (
	"maps"
	"regexp"
	"slices"
)

var (
	// Enterprise-grid W IDs count as users here, unlike target parsing.
	referencedUserIDRe      = regexp.MustCompile(`^[UW][A-Z0-9]{8,}$`)
	referencedChannelIDRe   = regexp.MustCompile(`^[CG][A-Z0-9]{8,}$`)
	referencedUsergroupIDRe = regexp.MustCompile(`^S[A-Z0-9]{8,}$`)

	mentionTokenRe          = regexp.MustCompile(`<@([UW][A-Z0-9]{8,})(?:\|[^>]+)?>`)
	channelMentionTokenRe   = regexp.MustCompile(`<#([CG][A-Z0-9]{8,})(?:\|[^>]+)?>`)
	usergroupMentionTokenRe = regexp.MustCompile(`<!subteam\^([S][A-Z0-9]{8,})(?:\|[^>]+)?>`)
)

// IsReferencedUserID reports whether s is a user ID as referenced in message
// payloads — including enterprise-grid "W…" IDs, unlike target parsing's
// IsUserID (which accepts only "U…").
func IsReferencedUserID(s string) bool { return referencedUserIDRe.MatchString(s) }

// IsReferencedChannelID reports whether s is a channel/group ID (C…/G…).
func IsReferencedChannelID(s string) bool { return referencedChannelIDRe.MatchString(s) }

// IsReferencedUsergroupID reports whether s is a usergroup (subteam) ID (S…).
func IsReferencedUsergroupID(s string) bool { return referencedUsergroupIDRe.MatchString(s) }

// ReferencedIDs holds the distinct entity ids a set of messages refers to. A
// rich_text mention element carries only the bare id (no label), so resolving
// these is the only way to make <@U…>/<#C…>/<!subteam^S…> mentions legible.
type ReferencedIDs struct {
	Users      []string
	Channels   []string
	Usergroups []string
}

// CollectReferencedIDs gathers every user, channel, and usergroup id a set of
// messages refers to — authorship, mention tokens in text, and id fields
// anywhere in blocks and attachments (and, optionally, reaction user lists) —
// in a single tree-walk. Order is first-seen; map walks are key-sorted for
// determinism.
func CollectReferencedIDs(messages []MessageSummary, includeReactions bool) ReferencedIDs {
	a := newIDAccumulator()
	refs := refCollector{addU: a.addUser, addC: a.addChannel, addG: a.addGroup}

	for _, msg := range messages {
		a.addUser(msg.User)
		refs.fromText(msg.Text)
		for _, b := range msg.Blocks {
			refs.fromValue(b)
		}
		for _, at := range msg.Attachments {
			refs.fromValue(at)
		}
		if includeReactions {
			for _, rx := range msg.Reactions {
				refs.fromValue(rx)
			}
		}
	}
	return a.refs
}

// idAccumulator gathers distinct user/channel/usergroup ids in first-seen order,
// each filtered by its IsReferenced* validity check. Shared by CollectReferencedIDs
// (a block/text tree-walk) and CollectDisplayIDs (a post-render token scan) so the
// dedup bookkeeping and the validity gate live in one place.
type idAccumulator struct {
	refs                ReferencedIDs
	seenU, seenC, seenG map[string]bool
}

func newIDAccumulator() *idAccumulator {
	return &idAccumulator{seenU: map[string]bool{}, seenC: map[string]bool{}, seenG: map[string]bool{}}
}

func (a *idAccumulator) addUser(id string) {
	if IsReferencedUserID(id) && !a.seenU[id] {
		a.seenU[id] = true
		a.refs.Users = append(a.refs.Users, id)
	}
}

func (a *idAccumulator) addChannel(id string) {
	if IsReferencedChannelID(id) && !a.seenC[id] {
		a.seenC[id] = true
		a.refs.Channels = append(a.refs.Channels, id)
	}
}

func (a *idAccumulator) addGroup(id string) {
	if IsReferencedUsergroupID(id) && !a.seenG[id] {
		a.seenG[id] = true
		a.refs.Usergroups = append(a.refs.Usergroups, id)
	}
}

// refCollector threads the three per-type add funcs through one recursive walk.
type refCollector struct {
	addU, addC, addG func(string)
}

func (r refCollector) fromText(text string) {
	for _, m := range mentionTokenRe.FindAllStringSubmatch(text, -1) {
		r.addU(m[1])
	}
	for _, m := range channelMentionTokenRe.FindAllStringSubmatch(text, -1) {
		r.addC(m[1])
	}
	for _, m := range usergroupMentionTokenRe.FindAllStringSubmatch(text, -1) {
		r.addG(m[1])
	}
}

func (r refCollector) fromValue(value any) {
	switch v := value.(type) {
	case string:
		r.fromText(v)
	case []any:
		for _, item := range v {
			r.fromValue(item)
		}
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			child := v[key]
			switch key {
			case "user", "user_id":
				if id, ok := child.(string); ok {
					r.addU(id)
					continue
				}
			case "users":
				for _, u := range asSlice(child) {
					if id, ok := u.(string); ok {
						r.addU(id)
					}
				}
				continue
			case "channel_id":
				if id, ok := child.(string); ok {
					r.addC(id)
					continue
				}
			case "usergroup_id":
				if id, ok := child.(string); ok {
					r.addG(id)
					continue
				}
			}
			r.fromValue(child)
		}
	}
}
