package slack

import "time"

// Drafts and scheduled messages share a write-only completion cache: their
// list/cancel paths always hit the API (either can change at any time, so a
// stale read would be wrong), but each list write-warms its category so shell
// completion can suggest ids (Dr… / scheduled ids) without an API call. Nothing
// reads these caches except ReadCompletions. Both project to the same {id, text}
// shape, so they share one machinery and differ only in the source they warm
// from.

// compactIDText is the completion-sized projection shared by the drafts and
// scheduled-message caches: the id (the completion value) and text (its
// description).
type compactIDText struct {
	ID   string `json:"id"`
	Text string `json:"text,omitempty"`
}

func validIDText(_ string, e compactIDText) bool { return e.ID != "" }

// warmIDTextCache records the ids a list just returned so later completions can
// offer them. Batched (one save) and best-effort; ids no longer listed age out
// at the category TTL.
func warmIDTextCache(c *Client, category string, ttl time.Duration, items []compactIDText) {
	snap := openCacheFor(c, category, ttl, validIDText)
	for _, e := range items {
		if validIDText(e.ID, e) {
			snap.set(e.ID, e)
		}
	}
	snap.save()
}

// warmDraftCache write-warms the "drafts" category from a plain-draft list.
func (c *Client) warmDraftCache(drafts []Draft) {
	items := make([]compactIDText, 0, len(drafts))
	for _, d := range drafts {
		items = append(items, compactIDText{ID: d.ID, Text: d.Text})
	}
	warmIDTextCache(c, cacheCategoryDrafts, cacheTTLOf(c.cache).Drafts, items)
}

// warmScheduledCache write-warms the "scheduled" category from a scheduled-list
// page. The raw map shape works across the browser and bot-token list bodies.
func (c *Client) warmScheduledCache(items []map[string]any) {
	entries := make([]compactIDText, 0, len(items))
	for _, m := range items {
		entries = append(entries, compactIDText{ID: getStr(m, "id"), Text: getStr(m, "text")})
	}
	warmIDTextCache(c, cacheCategoryScheduled, cacheTTLOf(c.cache).Scheduled, entries)
}
