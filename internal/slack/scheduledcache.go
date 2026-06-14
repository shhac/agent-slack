package slack

// Scheduled messages are write-only cached: `scheduled list`/`cancel` always
// hit the API (a scheduled message can be cancelled or delivered at any time, so
// a stale read would be wrong), but each list write-warms this category so shell
// completion can suggest scheduled-message ids without credentials or an API
// call. Nothing reads this cache except ReadCompletions.

// CompactScheduled is the completion-sized projection of one scheduled message:
// only the id (the completion value) and text (its description). Both are
// strings, so warming is type-agnostic across the browser and bot-token list
// shapes; nothing else about a scheduled message drives completion.
type CompactScheduled struct {
	ID   string `json:"id"`
	Text string `json:"text,omitempty"`
}

func validScheduled(_ string, s CompactScheduled) bool { return s.ID != "" }

func (c *Client) scheduledCache() *cacheSnapshot[CompactScheduled] {
	return openCacheFor(c, "scheduled", cacheTTLOf(c.cache).Scheduled, validScheduled)
}

// warmScheduledCache records the ids a `scheduled list` just fetched so later
// completions can offer them. Batched (one save) and best-effort; ids no longer
// listed age out at the category TTL.
func (c *Client) warmScheduledCache(items []map[string]any) {
	snap := c.scheduledCache()
	for _, m := range items {
		s := CompactScheduled{ID: getStr(m, "id"), Text: getStr(m, "text")}
		if validScheduled(s.ID, s) {
			snap.set(s.ID, s)
		}
	}
	snap.save()
}
