package slack

import (
	"context"
	"regexp"
	"strings"
	"sync"
)

const fetchConcurrency = 5

// Mention collection accepts W (enterprise) IDs as well as U.
var cacheUserIDRe = regexp.MustCompile(`^[UW][A-Z0-9]{8,}$`)

// ResolveUsersByID expands user IDs to compact profiles via the Client's
// per-workspace cache, best effort: IDs that fail to fetch are absent from the
// result, and cache I/O never fails the command. forceRefresh ignores cached
// reads but still writes fresh entries (the per-command --refresh-users, which
// the caller ORs with the global --refresh-cache mode).
func ResolveUsersByID(ctx context.Context, c *Client, userIDs []string, forceRefresh bool) map[string]CompactUser {
	ids := dedupeUserIDs(userIDs)
	out := make(map[string]CompactUser, len(ids))
	if len(ids) == 0 {
		return out
	}

	snap := openCache[CompactUser](c.cache, "users", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).Users,
		func(id string, u CompactUser) bool { return cacheUserIDRe.MatchString(id) && u.ID != "" })

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

func dedupeUserIDs(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if !cacheUserIDRe.MatchString(id) || seen[id] {
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
