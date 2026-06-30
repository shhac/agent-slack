package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Introspection + purge for the `cache` CLI command. The cache layout
// (<dir>/<team_id>/<user_id>/<category>.json) is owned here, so the management
// surface lives here too. A "key" below is the two-segment identity subpath
// <team_id>/<user_id> that IdentityCacheKey builds.

// CacheCategory summarizes one category file without decoding its value type.
type CacheCategory struct {
	Category string `json:"category"`
	Entries  int    `json:"entries"`
	Bytes    int64  `json:"bytes"`
	OldestMS int64  `json:"oldest_fetched_at_ms,omitempty"`
	NewestMS int64  `json:"newest_fetched_at_ms,omitempty"`
}

// CachedIdentityKeys lists the per-identity subdirectory keys (<team_id>/
// <user_id>) present in the cache dir. It walks the two-level layout: each
// team dir, then each user dir beneath it.
func CachedIdentityKeys(cacheDir string) ([]string, error) {
	if cacheDir == "" {
		return nil, nil
	}
	teams, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, team := range teams {
		if !team.IsDir() || strings.HasPrefix(team.Name(), ".") {
			continue
		}
		users, err := os.ReadDir(filepath.Join(cacheDir, team.Name()))
		if err != nil {
			continue
		}
		for _, user := range users {
			if user.IsDir() {
				out = append(out, filepath.Join(team.Name(), user.Name()))
			}
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

// The per-identity subtree holds two well-known subdirectories beside its
// category files; the slack package owns these names so the CLI path helpers
// reference one source of truth.
const (
	// DownloadsSubdir holds downloaded files; kept by a resolution-cache purge
	// (cleared only by --downloads or a full identity purge).
	DownloadsSubdir = "downloads"
	// EmojiImagesSubdir holds decoded custom-emoji PNGs.
	EmojiImagesSubdir = "emoji-images"
)

// PurgeCacheDir clears one identity's regenerable resolution cache (key is
// <team_id>/<user_id>): every category file and the emoji-image cache, but NOT
// its downloads — those are user artifacts cleared separately. Empty parent
// directories are pruned so a fully-cleared identity (and its team, if it had no
// siblings) leaves nothing behind.
func PurgeCacheDir(cacheDir, key string) error {
	if cacheDir == "" || key == "" {
		return nil
	}
	dir := filepath.Join(cacheDir, key)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.Name() == DownloadsSubdir {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	pruneEmptyIdentityDirs(cacheDir, key)
	return nil
}

// PurgeIdentityDir removes one identity's entire subtree — resolution cache,
// downloads, and emoji images — for use when its credential is removed. Empty
// parent directories are pruned.
func PurgeIdentityDir(cacheDir, key string) error {
	if cacheDir == "" || key == "" {
		return nil
	}
	if err := os.RemoveAll(filepath.Join(cacheDir, key)); err != nil {
		return err
	}
	pruneEmptyIdentityDirs(cacheDir, key)
	return nil
}

// PurgeDownloads removes one identity's downloaded files (key is
// <team_id>/<user_id>), leaving its resolution cache untouched.
func PurgeDownloads(cacheDir, key string) error {
	if cacheDir == "" || key == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(cacheDir, key, DownloadsSubdir))
}

// PurgeAllDownloads removes every cached identity's downloaded files. Mirrors
// PurgeAllCaches so the slack package owns both all-identity purge sweeps.
func PurgeAllDownloads(cacheDir string) error {
	keys, err := CachedIdentityKeys(cacheDir)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := PurgeDownloads(cacheDir, k); err != nil {
			return err
		}
	}
	return nil
}

// PurgeAllCaches clears every per-identity resolution cache (each identity's
// downloads kept), then sweeps any pre-identity-layout orphans so a full purge
// leaves nothing regenerable-but-unreachable behind.
func PurgeAllCaches(cacheDir string) ([]string, error) {
	keys, err := CachedIdentityKeys(cacheDir)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if err := PurgeCacheDir(cacheDir, k); err != nil {
			return nil, err
		}
	}
	purgeLegacyArtifacts(cacheDir)
	return keys, nil
}

// legacyHostHashRe matches a pre-identity per-workspace cache dir name (a
// 16-hex host hash). Current team-id dirs (T…/E…) never match.
var legacyHostHashRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// purgeLegacyArtifacts removes cache directories an older (pre-identity) layout
// left at the cache root — the old <host-hash>/ resolution dirs and the old flat
// downloads/ and emoji-images/, plus the defunct .layout-v2 sentinel — so an
// --all-workspaces purge leaves no orphaned cache. The current layout nests
// downloads/emoji-images under <team>/<user>/, so the top-level names only ever
// match legacy artifacts. Best-effort.
func purgeLegacyArtifacts(cacheDir string) {
	if cacheDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(cacheDir, ".layout-v2"))
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && isLegacyArtifactName(e.Name()) {
			_ = os.RemoveAll(filepath.Join(cacheDir, e.Name()))
		}
	}
}

// isLegacyArtifactName reports whether a cache-root directory name belongs to
// the pre-identity layout: an old <host-hash>/ resolution dir, or the old flat
// downloads/ and emoji-images/. Current-layout team dirs (uppercase T…/E…) never
// match the host-hash pattern, and downloads/emoji-images now nest under an
// identity, so this can only ever name a legacy artifact.
func isLegacyArtifactName(name string) bool {
	return legacyHostHashRe.MatchString(name) || name == DownloadsSubdir || name == EmojiImagesSubdir
}

// pruneEmptyIdentityDirs removes the identity dir and then its team parent, each
// only if now empty — best-effort, so a surviving sibling or downloads dir keeps
// the parent in place.
func pruneEmptyIdentityDirs(cacheDir, key string) {
	_ = os.Remove(filepath.Join(cacheDir, key)) // identity dir, if empty
	if team := firstSegment(key); team != "" {
		_ = os.Remove(filepath.Join(cacheDir, team)) // team dir, if empty
	}
}

// firstSegment returns the leading path element of a key ("T123/U456" → "T123").
func firstSegment(key string) string {
	if i := strings.IndexByte(key, filepath.Separator); i >= 0 {
		return key[:i]
	}
	return key
}
