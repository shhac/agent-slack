package slack

import (
	"context"
	"strings"
)

// ResolveUsergroupID maps a usergroup handle (@marketing, marketing) to its
// subteam id (S…). On a cache miss it fetches the full usergroups.list once and
// warms every handle, so later lookups are free. Returns "" with no error when
// no usergroup matches (a not-found must not be cached against the TTL).
func ResolveUsergroupID(ctx context.Context, c *Client, input string) (string, error) {
	key := handleCacheKey(input)
	if key == "" {
		return "", nil
	}
	if id, ok := c.cachedUsergroupIDByHandle(key); ok {
		return id, nil
	}

	resp, err := c.API(ctx, "usergroups.list", map[string]any{})
	if err != nil {
		return "", err
	}
	byHandle := map[string]string{}
	for _, g := range recItems(getArr(resp, "usergroups")) {
		handle := strings.ToLower(getStr(g, "handle"))
		id := getStr(g, "id")
		if handle != "" && id != "" {
			byHandle[handle] = id
		}
	}
	c.warmUsergroupCache(byHandle)
	return byHandle[key], nil
}
