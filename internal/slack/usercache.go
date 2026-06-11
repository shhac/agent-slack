package slack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The CLI cold-starts on every agent invocation, so an in-memory cache would
// be useless — user profiles persist on disk with a TTL instead, keyed by a
// hash of the workspace host (one cache file per workspace, no URL in the
// filename).
const (
	userCacheVersion = 1
	userCacheTTL     = 24 * time.Hour
	fetchConcurrency = 5
)

// Mention collection accepts W (enterprise) IDs as well as U.
var cacheUserIDRe = regexp.MustCompile(`^[UW][A-Z0-9]{8,}$`)

// ResolveUsersOptions controls ResolveUsersByID.
type ResolveUsersOptions struct {
	// CacheDir hosts the per-workspace cache files; "" disables disk caching.
	CacheDir string
	// ForceRefresh ignores cached entries (still writes fresh ones).
	ForceRefresh bool
	// Now overrides time.Now for tests.
	Now time.Time
}

// ResolveUsersByID expands user IDs to compact profiles, best effort: IDs
// that fail to fetch are simply absent from the result, and cache I/O errors
// are swallowed (a cache must never fail the command).
func ResolveUsersByID(ctx context.Context, c *Client, workspaceURL string, userIDs []string, opts ResolveUsersOptions) map[string]CompactUser {
	ids := dedupeUserIDs(userIDs)
	if len(ids) == 0 {
		return map[string]CompactUser{}
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	cachePath := userCachePath(opts.CacheDir, workspaceURL)
	cache := loadUserCache(cachePath)

	out := make(map[string]CompactUser, len(ids))
	var missing []string
	for _, id := range ids {
		entry, ok := cache.Entries[id]
		if ok && !opts.ForceRefresh && now.UnixMilli()-entry.FetchedAt < userCacheTTL.Milliseconds() {
			out[id] = entry.User
			continue
		}
		missing = append(missing, id)
	}

	cacheChanged := false
	if len(missing) > 0 {
		for id, user := range fetchUsersByID(ctx, c, missing) {
			cache.Entries[id] = userCacheEntry{FetchedAt: now.UnixMilli(), User: user}
			out[id] = user
			cacheChanged = true
		}
	}

	if cachePath != "" {
		if pruneExpiredUsers(cache, now) {
			cacheChanged = true
		}
		if cacheChanged {
			writeUserCache(cachePath, cache)
		}
	}

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
			user, ok := resp["user"].(map[string]any)
			if !ok {
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

type userCacheEntry struct {
	FetchedAt int64       `json:"fetched_at"` // unix milliseconds
	User      CompactUser `json:"user"`
}

type userCacheFile struct {
	Version int                       `json:"version"`
	Entries map[string]userCacheEntry `json:"entries"`
}

func userCachePath(cacheDir, workspaceURL string) string {
	if cacheDir == "" {
		return ""
	}
	key := hashWorkspaceURL(workspaceURL)
	if key == "" {
		return ""
	}
	return filepath.Join(cacheDir, "users-cache-"+key+".json")
}

func hashWorkspaceURL(workspaceURL string) string {
	trimmed := strings.TrimSpace(workspaceURL)
	if trimmed == "" {
		return ""
	}
	source := strings.ToLower(trimmed)
	if u, err := url.Parse(trimmed); err == nil && u.Hostname() != "" {
		source = strings.ToLower(u.Hostname())
	}
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])[:16]
}

func loadUserCache(path string) *userCacheFile {
	fresh := &userCacheFile{Version: userCacheVersion, Entries: map[string]userCacheEntry{}}
	if path == "" {
		return fresh
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fresh
	}
	var file userCacheFile
	if err := json.Unmarshal(raw, &file); err != nil || file.Version != userCacheVersion || file.Entries == nil {
		return fresh
	}
	for id, entry := range file.Entries {
		if !cacheUserIDRe.MatchString(id) || entry.FetchedAt <= 0 || entry.User.ID == "" {
			delete(file.Entries, id)
		}
	}
	return &file
}

func writeUserCache(path string, file *userCacheFile) {
	raw, err := json.Marshal(file)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600) // best effort
}

func pruneExpiredUsers(file *userCacheFile, now time.Time) bool {
	changed := false
	for _, id := range sortedKeys(file.Entries) {
		if now.UnixMilli()-file.Entries[id].FetchedAt >= userCacheTTL.Milliseconds() {
			delete(file.Entries, id)
			changed = true
		}
	}
	return changed
}

func sortedKeys(m map[string]userCacheEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
