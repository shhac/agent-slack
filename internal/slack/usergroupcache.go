package slack

// Usergroups (subteams) are addressed by handle (e.g. @marketing) but mentioned
// by id (<!subteam^S…>). usergroups.list returns the whole set in one call, so a
// miss warms every handle→id mapping at once. Handles rarely change, hence a 24h
// TTL like users.

func (c *Client) usergroupsCache() *cacheSnapshot[string] {
	return openCacheFor[string](c, "usergroups", cacheTTLOf(c.cache).Usergroups, nil)
}

func (c *Client) cachedUsergroupIDByHandle(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	return c.usergroupsCache().get(key)
}

// warmUsergroupCache records handle→id mappings a usergroups.list returned.
// Batched (one save) and best-effort.
func (c *Client) warmUsergroupCache(byHandle map[string]string) {
	snap := c.usergroupsCache()
	for handle, id := range byHandle {
		if handle != "" && id != "" {
			snap.set(handle, id)
		}
	}
	snap.save()
}
