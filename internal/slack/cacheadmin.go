package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Introspection + purge for the `cache` CLI command. The cache layout
// (<dir>/<wshash>/<category>.json) is owned here, so the management surface
// lives here too.

// CacheCategory summarizes one category file without decoding its value type.
type CacheCategory struct {
	Category string `json:"category"`
	Entries  int    `json:"entries"`
	Bytes    int64  `json:"bytes"`
	OldestMS int64  `json:"oldest_fetched_at_ms,omitempty"`
	NewestMS int64  `json:"newest_fetched_at_ms,omitempty"`
}

// WorkspaceCacheKey is the per-workspace cache subdirectory name for a URL.
func WorkspaceCacheKey(workspaceURL string) string { return hashWorkspaceURL(workspaceURL) }

// CachedWorkspaceKeys lists the per-workspace subdirectory names present in the
// cache dir (excluding the downloads dir).
func CachedWorkspaceKeys(cacheDir string) ([]string, error) {
	if cacheDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "downloads" {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out, nil
}

// InspectCacheDir reports the category files under one workspace subdirectory
// (cacheDir/<key>), each with its entry count, size, and fetched-at range.
func InspectCacheDir(cacheDir, key string) ([]CacheCategory, error) {
	if cacheDir == "" || key == "" {
		return nil, nil
	}
	dir := filepath.Join(cacheDir, key)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CacheCategory
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		stat := readCacheStat(filepath.Join(dir, e.Name()))
		stat.Category = strings.TrimSuffix(e.Name(), ".json")
		out = append(out, stat)
	}
	slices.SortFunc(out, func(a, b CacheCategory) int { return strings.Compare(a.Category, b.Category) })
	return out, nil
}

// readCacheStat counts entries and the fetched-at range of one cache file
// without knowing the value type (it reads only the envelope).
func readCacheStat(path string) CacheCategory {
	var c CacheCategory
	if fi, err := os.Stat(path); err == nil {
		c.Bytes = fi.Size()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var data struct {
		Entries map[string]struct {
			FetchedAt int64 `json:"fetched_at"`
		} `json:"entries"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return c
	}
	c.Entries = len(data.Entries)
	for _, e := range data.Entries {
		if c.OldestMS == 0 || e.FetchedAt < c.OldestMS {
			c.OldestMS = e.FetchedAt
		}
		if e.FetchedAt > c.NewestMS {
			c.NewestMS = e.FetchedAt
		}
	}
	return c
}

// PurgeCacheDir removes one workspace's cache subdirectory.
func PurgeCacheDir(cacheDir, key string) error {
	if cacheDir == "" || key == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(cacheDir, key))
}

// PurgeAllCaches removes every per-workspace cache subdirectory (not downloads).
func PurgeAllCaches(cacheDir string) ([]string, error) {
	keys, err := CachedWorkspaceKeys(cacheDir)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if err := os.RemoveAll(filepath.Join(cacheDir, k)); err != nil {
			return nil, err
		}
	}
	return keys, nil
}
