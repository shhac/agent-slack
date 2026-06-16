package slack

import (
	"context"
	"strings"
	"sync"

	"github.com/shhac/agent-slack/internal/render"
)

const fetchConcurrency = 5

// validUser gates which cache entries count: a referenced user id with a body.
func validUser(id string, u CompactUser) bool {
	return render.IsReferencedUserID(id) && u.ID != ""
}

func (c *Client) usersCache() *cacheSnapshot[CompactUser] {
	return openCacheFor(c, "users", cacheTTLOf(c.cache).Users, validUser)
}

// warmUserCache records profiles a list command already fetched, so user
// completions and later ID→profile lookups are populated without their own
// API calls. It also fills the handles index (handle→id) from each user's
// name, so `@handle` resolution (ResolveUserID) is a cache hit after a warm —
// the resolver reads the handles cache, which the entity store alone never
// populated. When complete is set (the caller enumerated every user, bots
// included), the handles index is marked complete so a later miss is
// authoritative. Batched (one save per store) and best-effort.
func (c *Client) warmUserCache(users []CompactUser, complete bool) {
	snap := c.usersCache()
	handles := c.handlesCache()
	for _, u := range users {
		if !validUser(u.ID, u) {
			continue
		}
		snap.set(u.ID, u)
		if key := handleCacheKey(u.Name); key != "" {
			handles.set(key, u.ID)
		}
	}
	if complete {
		handles.markComplete()
	}
	snap.save()
	handles.save()
}

// usersComplete reports whether the users category was fully enumerated within
// the completeness window — so an @handle miss is authoritative.
func (c *Client) usersComplete() bool {
	return c.handlesCache().isComplete(cacheTTLOf(c.cache).UsersComplete)
}

// ResolveUsersByID expands user ids to compact profiles per the policy, reading
// the per-workspace cache and fetching misses (users.info) unless the policy or
// the completeness sentinel says not to. Returns whether it made an API fetch.
func ResolveUsersByID(ctx context.Context, c *Client, userIDs []string, policy ResolvePolicy) (map[string]CompactUser, bool) {
	ids := dedupeUserIDs(userIDs)
	out := make(map[string]CompactUser, len(ids))
	if len(ids) == 0 {
		return out, false
	}

	snap := c.usersCache()

	var missing []string
	for _, id := range ids {
		if policy != ResolveBypassCache {
			if u, ok := snap.get(id); ok {
				out[id] = u
				continue
			}
		}
		missing = append(missing, id)
	}

	fetched := false
	if len(missing) > 0 && policy.wantFetch(c.usersComplete()) {
		for id, user := range fetchUsersByID(ctx, c, missing) {
			snap.set(id, user)
			out[id] = user
		}
		fetched = true
	}

	snap.save()
	return out, fetched
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
