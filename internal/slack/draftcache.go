package slack

// Drafts are write-only cached for completion: listDrafts always hits the API (a
// draft can be edited, sent, or deleted at any time, so a stale read would be
// wrong), but each plain-draft list warms this category so shell completion can
// suggest draft ids (Dr…) for get/edit/delete/send without an API call. Nothing
// reads this cache except ReadCompletions — it mirrors the scheduled-id cache.

// CompactDraft is the completion-sized projection of one draft: the id (the
// completion value) and text (its description).
type CompactDraft struct {
	ID   string `json:"id"`
	Text string `json:"text,omitempty"`
}

func validDraft(_ string, d CompactDraft) bool { return d.ID != "" }

func (c *Client) draftCache() *cacheSnapshot[CompactDraft] {
	return openCacheFor(c, "drafts", cacheTTLOf(c.cache).Drafts, validDraft)
}

// warmDraftCache records the ids a plain-draft list just returned so later
// completions can offer them. Batched (one save) and best-effort; ids no longer
// listed age out at the category TTL.
func (c *Client) warmDraftCache(drafts []Draft) {
	snap := c.draftCache()
	for _, d := range drafts {
		cd := CompactDraft{ID: d.ID, Text: d.Text}
		if validDraft(cd.ID, cd) {
			snap.set(cd.ID, cd)
		}
	}
	snap.save()
}
