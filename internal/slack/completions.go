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
	CompleteScheduled
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
// per-workspace caches, most-recently-cached first, capped at limit.
//
// Each entity is offered in several value-forms so whatever the user typed has
// a matching candidate — a channel as `#name`, its id, and the bare `name`; a
// user as `@handle`, its id, and the bare `handle`. A candidate is kept only if
// its VALUE prefix-matches what was typed (the shell applies the same filter,
// so a non-matching value would be hidden anyway), and on a bare tab (no input)
// only the primary form is offered to avoid three lines per entity.
func ReadCompletions(cacheDir, workspaceURL, toComplete string, limit int, sources CompletionSource) []CompletionItem {
	type ranked struct {
		item    CompletionItem
		fetched int64
	}
	var all []ranked
	needle := strings.ToLower(toComplete)
	seen := map[string]bool{}

	add := func(value, desc string, fetched int64) {
		if value == "" || seen[value] {
			return
		}
		if needle != "" && !strings.HasPrefix(strings.ToLower(value), needle) {
			return
		}
		seen[value] = true
		all = append(all, ranked{CompletionItem{Value: value, Description: desc}, fetched})
	}
	// addForms offers an entity's alternate value-forms (primary first). With no
	// input typed, only the primary form is added.
	addForms := func(desc string, fetched int64, forms ...string) {
		if needle == "" {
			add(forms[0], desc, fetched)
			return
		}
		for _, f := range forms {
			add(f, desc, fetched)
		}
	}

	if sources&CompleteChannels != 0 {
		// Entity store first (has the topic), then the name→ID index, which the
		// common search-resolution path populates even when no full channel
		// object was ever fetched. Forms: #name, id, name.
		for _, e := range loadCacheEntries[CompactChannel](cacheDir, workspaceURL, "channels") {
			ch := e.Value
			if ch.IsIM || ch.Name == "" {
				continue // DMs have no stable name to complete
			}
			addForms(ch.Topic, e.FetchedAt, "#"+ch.Name, ch.ID, ch.Name)
		}
		for name, e := range loadCacheEntries[string](cacheDir, workspaceURL, "channel-names") {
			addForms("", e.FetchedAt, "#"+name, e.Value, name) // e.Value is the id
		}
	}
	if sources&CompleteUsers != 0 {
		for _, e := range loadCacheEntries[CompactUser](cacheDir, workspaceURL, "users") {
			u := e.Value
			realName := firstNonEmpty(u.RealName, u.DisplayName)
			if u.Name == "" {
				add(u.ID, realName, e.FetchedAt) // no handle — id only
				continue
			}
			// Each form's hint shows the OTHER two datapoints: a handle/name form
			// shows the id, the id form shows the handle.
			withID := userHint(realName, "("+u.ID+")")
			withHandle := userHint(realName, "(@"+u.Name+")")
			add("@"+u.Name, withID, e.FetchedAt) // primary
			if needle != "" {
				add(u.ID, withHandle, e.FetchedAt)
				add(u.Name, withID, e.FetchedAt)
			}
		}
	}
	if sources&CompleteTriggers != 0 {
		for id, e := range loadCacheEntries[WorkflowPreview](cacheDir, workspaceURL, "workflow-triggers") {
			add(id, firstNonEmpty(e.Value.Name, e.Value.Workflow.Title), e.FetchedAt)
		}
	}
	if sources&CompleteScheduled != 0 {
		for id, e := range loadCacheEntries[CompactScheduled](cacheDir, workspaceURL, "scheduled") {
			add(id, e.Value.Text, e.FetchedAt)
		}
	}

	// Most-recently-fetched first; ties broken alphabetically by value. The
	// alphabetical tiebreak is load-bearing: a bulk warm (e.g. `user list`)
	// stamps every entry with the same fetched_at, so without it the order —
	// and, once the cap truncates, which entries even survive — would follow
	// Go's randomized map iteration and shuffle on every keystroke.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].fetched != all[j].fetched {
			return all[i].fetched > all[j].fetched
		}
		return strings.ToLower(all[i].item.Value) < strings.ToLower(all[j].item.Value)
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]CompletionItem, len(all))
	for i, r := range all {
		out[i] = r.item
	}
	return out
}

// userHint joins a real name with a parenthetical of the other identifier,
// dropping the name when absent.
func userHint(realName, paren string) string {
	if realName == "" {
		return paren
	}
	return realName + " " + paren
}
