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
	CompleteUsergroups
	CompleteDrafts
)

// loadCacheEntries reads one category file for a workspace directly (ignoring
// TTL — completions surface even slightly-stale hints) and returns its entries
// with their fetched_at timestamps. Pure read; no API, no credentials.
func loadCacheEntries[T any](cacheDir, workspaceURL, category string) map[string]cacheEntry[T] {
	data := readCacheFile[T](cacheFilePath(cacheDir, workspaceURL, category))
	if data == nil {
		return nil
	}
	return data.Entries
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
	col := newCompletionCollector(cacheDir, workspaceURL, toComplete)
	if sources&CompleteChannels != 0 {
		col.addChannels()
	}
	if sources&CompleteUsers != 0 {
		col.addUsers()
	}
	if sources&CompleteUsergroups != 0 {
		col.addUsergroups()
	}
	if sources&CompleteTriggers != 0 {
		col.addTriggers()
	}
	if sources&CompleteScheduled != 0 {
		col.addIDText("scheduled")
	}
	if sources&CompleteDrafts != 0 {
		col.addIDText("drafts")
	}
	return col.rank(limit)
}

// completionCollector accumulates ranked candidates from one or more sources,
// deduping by value and prefix-filtering against the typed input. It replaces a
// trio of captured closures so each source is an independently testable method.
type completionCollector struct {
	cacheDir     string
	workspaceURL string
	needle       string
	seen         map[string]bool
	all          []ranked
}

type ranked struct {
	item    CompletionItem
	fetched int64
}

func newCompletionCollector(cacheDir, workspaceURL, toComplete string) *completionCollector {
	return &completionCollector{
		cacheDir:     cacheDir,
		workspaceURL: workspaceURL,
		needle:       strings.ToLower(toComplete),
		seen:         map[string]bool{},
	}
}

func (c *completionCollector) add(value, desc string, fetched int64) {
	if value == "" || c.seen[value] {
		return
	}
	if c.needle != "" && !strings.HasPrefix(strings.ToLower(value), c.needle) {
		return
	}
	c.seen[value] = true
	c.all = append(c.all, ranked{CompletionItem{Value: value, Description: desc}, fetched})
}

// addForms offers an entity's alternate value-forms (primary first). With no
// input typed, only the primary form is added.
func (c *completionCollector) addForms(desc string, fetched int64, forms ...string) {
	if c.needle == "" {
		c.add(forms[0], desc, fetched)
		return
	}
	for _, f := range forms {
		c.add(f, desc, fetched)
	}
}

func (c *completionCollector) addChannels() {
	// Entity store first (has the topic), then the name→ID index, which the
	// common search-resolution path populates even when no full channel object
	// was ever fetched. Forms: #name, id, name.
	for _, e := range loadCacheEntries[CompactChannel](c.cacheDir, c.workspaceURL, "channels") {
		ch := e.Value
		if ch.IsIM || ch.Name == "" {
			continue // DMs have no stable name to complete
		}
		c.addForms(ch.Topic, e.FetchedAt, "#"+ch.Name, ch.ID, ch.Name)
	}
	for name, e := range loadCacheEntries[string](c.cacheDir, c.workspaceURL, "channel-names") {
		c.addForms("", e.FetchedAt, "#"+name, e.Value, name) // e.Value is the id
	}
}

func (c *completionCollector) addUsers() {
	for _, e := range loadCacheEntries[CompactUser](c.cacheDir, c.workspaceURL, "users") {
		u := e.Value
		realName := FirstNonEmpty(u.RealName, u.DisplayName)
		if u.Name == "" {
			c.add(u.ID, realName, e.FetchedAt) // no handle — id only
			continue
		}
		// Each form's hint shows the OTHER two datapoints: a handle/name form
		// shows the id, the id form shows the handle.
		withID := userHint(realName, "("+u.ID+")")
		withHandle := userHint(realName, "(@"+u.Name+")")
		c.add("@"+u.Name, withID, e.FetchedAt) // primary
		if c.needle != "" {
			c.add(u.ID, withHandle, e.FetchedAt)
			c.add(u.Name, withID, e.FetchedAt)
		}
	}
}

func (c *completionCollector) addUsergroups() {
	for _, e := range loadCacheEntries[CompactUsergroup](c.cacheDir, c.workspaceURL, "usergroup-entities") {
		g := e.Value
		desc := FirstNonEmpty(g.Name, g.Description)
		if g.Handle == "" {
			c.add(g.ID, desc, e.FetchedAt) // no handle — id only
			continue
		}
		// Forms: @handle (primary), id, bare handle.
		c.addForms(desc, e.FetchedAt, "@"+g.Handle, g.ID, g.Handle)
	}
}

func (c *completionCollector) addTriggers() {
	for id, e := range loadCacheEntries[WorkflowPreview](c.cacheDir, c.workspaceURL, "workflow-triggers") {
		c.add(id, FirstNonEmpty(e.Value.Name, e.Value.Workflow.Title), e.FetchedAt)
	}
}

// addIDText feeds a write-only {id, text} completion category (drafts,
// scheduled) into the collector.
func (c *completionCollector) addIDText(category string) {
	for id, e := range loadCacheEntries[compactIDText](c.cacheDir, c.workspaceURL, category) {
		c.add(id, e.Value.Text, e.FetchedAt)
	}
}

// rank orders candidates most-recently-fetched first, ties broken
// alphabetically by value, then caps at limit. The alphabetical tiebreak is
// load-bearing: a bulk warm (e.g. `user list`) stamps every entry with the same
// fetched_at, so without it the order — and, once the cap truncates, which
// entries even survive — would follow Go's randomized map iteration and shuffle
// on every keystroke.
func (c *completionCollector) rank(limit int) []CompletionItem {
	sort.SliceStable(c.all, func(i, j int) bool {
		if c.all[i].fetched != c.all[j].fetched {
			return c.all[i].fetched > c.all[j].fetched
		}
		return strings.ToLower(c.all[i].item.Value) < strings.ToLower(c.all[j].item.Value)
	})
	if limit > 0 && len(c.all) > limit {
		c.all = c.all[:limit]
	}
	out := make([]CompletionItem, len(c.all))
	for i, r := range c.all {
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
