package slack

import (
	"sort"
	"strings"
)

// CompletionItem is one shell-completion candidate: the Value to insert and a
// short Description (shown by zsh/fish v2 completions).
type CompletionItem struct {
	Value       string
	Description string
}

// CompletionSource selects which cached categories a completion draws from, so
// a --channel flag suggests only channels while a message <target> suggests
// channels and users.
type CompletionSource uint

const (
	CompleteChannels CompletionSource = 1 << iota
	CompleteUsers
	CompleteTriggers
)

// loadCacheEntries reads one category file for a workspace directly (ignoring
// TTL — completions surface even slightly-stale hints) and returns its entries
// with their fetched_at timestamps. Pure read; no API, no credentials.
func loadCacheEntries[T any](cacheDir, workspaceURL, category string) map[string]cacheEntry[T] {
	return readCacheFile[T](cacheFilePath(cacheDir, workspaceURL, category))
}

// ReadTargetCompletions returns channel and user <target> candidates — the
// common case for message targets that accept either.
func ReadTargetCompletions(cacheDir, workspaceURL, toComplete string, limit int) []CompletionItem {
	return ReadCompletions(cacheDir, workspaceURL, toComplete, limit, CompleteChannels|CompleteUsers)
}

// ReadCompletions returns candidates for the selected sources from the
// per-workspace caches, matching toComplete (case-insensitive prefix on the
// value or its label), most-recently-cached first, capped at limit. The cache
// fills as the user works, so this is empty on a cold cache and never blocks.
func ReadCompletions(cacheDir, workspaceURL, toComplete string, limit int, sources CompletionSource) []CompletionItem {
	type ranked struct {
		item    CompletionItem
		fetched int64
	}
	var all []ranked
	needle := strings.ToLower(strings.TrimPrefix(toComplete, "#"))
	seen := map[string]bool{}

	add := func(value, desc string, fetched int64, matchAgainst ...string) {
		if value == "" || seen[value] {
			return
		}
		if needle != "" && !anyHasPrefix(needle, matchAgainst) {
			return
		}
		seen[value] = true
		all = append(all, ranked{CompletionItem{Value: value, Description: desc}, fetched})
	}

	if sources&CompleteChannels != 0 {
		// Entity store first (richer metadata: topic), then the name→ID index,
		// which the common search-resolution path populates even when no full
		// channel object was ever fetched.
		for _, e := range loadCacheEntries[CompactChannel](cacheDir, workspaceURL, "channels") {
			ch := e.Value
			if ch.IsIM || ch.Name == "" {
				continue // DMs have no stable name to complete
			}
			label := "#" + ch.Name
			if ch.Topic != "" {
				label += " — " + ch.Topic
			}
			add("#"+ch.Name, label, e.FetchedAt, ch.Name, ch.ID)
		}
		for name, e := range loadCacheEntries[string](cacheDir, workspaceURL, "channel-names") {
			add("#"+name, "#"+name, e.FetchedAt, name, e.Value)
		}
	}
	if sources&CompleteUsers != 0 {
		for _, e := range loadCacheEntries[CompactUser](cacheDir, workspaceURL, "users") {
			u := e.Value
			desc := firstNonEmpty(u.DisplayName, u.RealName, u.Name)
			add(u.ID, desc, e.FetchedAt, u.ID, u.Name, u.DisplayName, u.RealName)
		}
	}
	if sources&CompleteTriggers != 0 {
		for id, e := range loadCacheEntries[WorkflowPreview](cacheDir, workspaceURL, "workflow-triggers") {
			p := e.Value
			add(id, firstNonEmpty(p.Name, p.Workflow.Title), e.FetchedAt, id, p.Name, p.Workflow.Title)
		}
	}

	sort.SliceStable(all, func(i, j int) bool { return all[i].fetched > all[j].fetched })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]CompletionItem, len(all))
	for i, r := range all {
		out[i] = r.item
	}
	return out
}

func anyHasPrefix(needle string, candidates []string) bool {
	for _, c := range candidates {
		if c != "" && strings.HasPrefix(strings.ToLower(c), needle) {
			return true
		}
	}
	return false
}
