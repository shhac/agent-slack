package render

import (
	"regexp"
	"sort"
)

var (
	// Enterprise-grid W IDs count as users here, unlike target parsing.
	referencedUserIDRe = regexp.MustCompile(`^[UW][A-Z0-9]{8,}$`)
	mentionTokenRe     = regexp.MustCompile(`<@([UW][A-Z0-9]{8,})(?:\|[^>]+)?>`)
)

// CollectReferencedUserIDs gathers every user ID a set of messages mentions —
// authorship, <@U…> tokens in text, user/user_id/users fields anywhere in
// blocks and attachments, and (optionally) reaction user lists — so
// --resolve-users knows what to expand. Order is first-seen; map walks are
// key-sorted for determinism.
func CollectReferencedUserIDs(messages []MessageSummary, includeReactions bool) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if referencedUserIDRe.MatchString(id) && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}

	for _, msg := range messages {
		add(msg.User)
		collectMentionIDs(msg.Text, add)
		for _, b := range msg.Blocks {
			collectUserIDsFromValue(b, add)
		}
		for _, a := range msg.Attachments {
			collectUserIDsFromValue(a, add)
		}
		if includeReactions {
			for _, r := range msg.Reactions {
				collectUserIDsFromValue(r, add)
			}
		}
	}
	return out
}

func collectMentionIDs(text string, add func(string)) {
	for _, m := range mentionTokenRe.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
}

func collectUserIDsFromValue(value any, add func(string)) {
	switch v := value.(type) {
	case string:
		collectMentionIDs(v, add)
	case []any:
		for _, item := range v {
			collectUserIDsFromValue(item, add)
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := v[key]
			switch key {
			case "user", "user_id":
				if id, ok := child.(string); ok {
					add(id)
					continue
				}
			case "users":
				for _, u := range asSlice(child) {
					if id, ok := u.(string); ok {
						add(id)
					}
				}
				continue
			}
			collectUserIDsFromValue(child, add)
		}
	}
}
