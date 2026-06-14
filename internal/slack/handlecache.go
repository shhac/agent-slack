package slack

import "strings"

// The handles cache maps a @handle/email to a user id (the name→id resolution
// behind ResolveUserID), the sibling of the usergroups cache that backs
// ResolveUsergroupID. Both are thin façades over the generic cacheSnapshot[T].

// handleCacheKey normalizes a @handle or email into its cache key (leading "@"
// stripped, lowercased).
func handleCacheKey(input string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(input), "@"))
}

func (c *Client) handlesCache() *cacheSnapshot[string] {
	return openCacheFor[string](c, "handles", cacheTTLOf(c.cache).Handles, nil)
}

func (c *Client) cachedUserIDByHandle(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	return c.handlesCache().get(key)
}

func (c *Client) cacheUserIDByHandle(key, id string) {
	cacheSet(c.handlesCache(), key, id, key != "" && id != "")
}
