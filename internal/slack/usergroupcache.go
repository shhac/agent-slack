package slack

// Usergroups (subteams) are addressed by handle (e.g. @marketing) but mentioned
// by id (<!subteam^S…>). usergroups.list returns the whole set in one call, so a
// miss warms every group at once. Handles rarely change, hence a 24h TTL like
// users. Two per-workspace caches back them, mirroring channels: a handle→id
// index ("usergroups") that resolution and mention-rewriting hit, and an
// id→CompactUsergroup entity store ("usergroup-entities") that `list`/`get` and
// completions read.

func validUsergroup(_ string, g CompactUsergroup) bool { return g.ID != "" }

func (c *Client) usergroupsCache() *cacheSnapshot[string] {
	return openCacheFor[string](c, "usergroups", cacheTTLOf(c.cache).Usergroups, nil)
}

func (c *Client) usergroupEntityCache() *cacheSnapshot[CompactUsergroup] {
	return openCacheFor(c, cacheCategoryUsergroupEntities, cacheTTLOf(c.cache).Usergroups, validUsergroup)
}

func (c *Client) cachedUsergroupIDByHandle(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	return c.usergroupsCache().get(key)
}

// usergroupsComplete reports whether every usergroup was enumerated within the
// completeness window — so a @group miss is authoritative.
func (c *Client) usergroupsComplete() bool {
	return c.usergroupsCache().isComplete(cacheTTLOf(c.cache).UsergroupsComplete)
}

// markUsergroupsComplete records a full usergroup enumeration on the handle
// index. Best-effort.
func (c *Client) markUsergroupsComplete() {
	snap := c.usergroupsCache()
	snap.markComplete()
	snap.save()
}

// warmUsergroups records the groups a usergroups.list returned into both the
// entity store (by id) and the handle→id index, so completions, name→id
// resolution, and `get` are all populated from one fetch. Batched (one save per
// store) and best-effort.
func (c *Client) warmUsergroups(groups []CompactUsergroup) {
	entity := c.usergroupEntityCache()
	names := c.usergroupsCache()
	for _, g := range groups {
		if g.ID == "" {
			continue
		}
		entity.set(g.ID, g)
		if key := handleCacheKey(g.Handle); key != "" {
			names.set(key, g.ID)
		}
	}
	entity.save()
	names.save()
}
