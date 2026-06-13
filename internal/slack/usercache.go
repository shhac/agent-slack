package slack

import (
	"context"
	"strings"
	"sync"

	"github.com/shhac/agent-slack/internal/render"
)

const fetchConcurrency = 5

// ResolveUsersByID expands user IDs to compact profiles via the Client's
// per-workspace cache, best effort: IDs that fail to fetch are absent from the
// result, and cache I/O never fails the command. forceRefresh ignores cached
// reads but still writes fresh entries (the per-command --refresh-users, which
// the caller ORs with the global --refresh-cache mode).
func validUser(id string, u CompactUser) bool {
	return render.IsReferencedUserID(id) && u.ID != ""
}

func (c *Client) usersCache() *cacheSnapshot[CompactUser] {
	return openCacheFor(c, "users", cacheTTLOf(c.cache).Users, validUser)
}

// warmUserCache records profiles a list command already fetched, so user
// completions and later ID→profile lookups are populated without their own
// API calls. Batched (one save) and best-effort.
func (c *Client) warmUserCache(users []CompactUser) {
	snap := c.usersCache()
	for _, u := range users {
		if validUser(u.ID, u) {
			snap.set(u.ID, u)
		}
	}
	snap.save()
}

func ResolveUsersByID(ctx context.Context, c *Client, userIDs []string, forceRefresh bool) map[string]CompactUser {
	ids := dedupeUserIDs(userIDs)
	out := make(map[string]CompactUser, len(ids))
	if len(ids) == 0 {
		return out
	}

	snap := c.usersCache()

	var missing []string
	for _, id := range ids {
		if !forceRefresh {
			if u, ok := snap.get(id); ok {
				out[id] = u
				continue
			}
		}
		missing = append(missing, id)
	}

	if len(missing) > 0 {
		for id, user := range fetchUsersByID(ctx, c, missing) {
			snap.set(id, user)
			out[id] = user
		}
	}

	snap.save()
	return out
}

// ToReferencedUsers shapes resolved users into the referenced_users output
// map, or nil when nothing resolved.
func ToReferencedUsers(userIDs []string, usersByID map[string]CompactUser) map[string]CompactUser {
	out := map[string]CompactUser{}
	for _, id := range dedupeUserIDs(userIDs) {
		if user, ok := usersByID[id]; ok {
			out[id] = user
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

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

func dedupeUserIDs(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if !render.IsReferencedUserID(id) || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func fetchUsersByID(ctx context.Context, c *Client, ids []string) map[string]CompactUser {
	var mu sync.Mutex
	out := make(map[string]CompactUser, len(ids))
	sem := make(chan struct{}, fetchConcurrency)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			resp, err := c.API(ctx, "users.info", map[string]any{"user": id})
			if err != nil {
				return // best effort
			}
			user := getRec(resp, "user")
			if user == nil {
				return
			}
			mu.Lock()
			out[id] = ToCompactUser(user)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}
