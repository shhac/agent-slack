package slack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The CLI cold-starts on every invocation, so resolutions that would otherwise
// be re-paid each run (channel name → ID, user handle → ID, profiles, workflow
// metadata) are persisted on disk, one JSON file per workspace per category:
// <cacheDir>/<wshash>-<category>.json. Message bodies are never cached.

// CacheMode controls the read/write behavior of the resolution cache.
type CacheMode int

const (
	CacheNormal  CacheMode = iota // read within TTL, write on miss
	CacheRefresh                  // skip reads, still write fresh entries
	CacheOff                      // no read, no write
)

// CacheTTL is the freshness window per category. A zero (or negative) duration
// disables reads for that category — every lookup misses — while writes still
// happen, so a later run with a non-zero TTL finds the data.
type CacheTTL struct {
	Users           time.Duration
	Channels        time.Duration
	ChannelNames    time.Duration
	Handles         time.Duration
	WorkflowPreview time.Duration
	WorkflowSchema  time.Duration
}

// DefaultCacheTTL is the built-in per-category freshness: stable profile data
// lasts a day; volatile name/membership/workflow mappings an hour.
func DefaultCacheTTL() CacheTTL {
	return CacheTTL{
		Users:           24 * time.Hour,
		Channels:        time.Hour,
		ChannelNames:    time.Hour,
		Handles:         time.Hour,
		WorkflowPreview: time.Hour,
		WorkflowSchema:  time.Hour,
	}
}

// Cache is the per-invocation resolution cache, built by the CLI and attached
// to a Client via WithCache. A nil *Cache, or one with an empty Dir, disables
// caching entirely (every snapshot is usable but inert).
type Cache struct {
	Dir  string
	Mode CacheMode
	TTL  CacheTTL
	now  func() time.Time
}

// NewCache builds a cache handle. now may be nil (defaults to time.Now).
func NewCache(dir string, mode CacheMode, ttl CacheTTL, now func() time.Time) *Cache {
	return &Cache{Dir: dir, Mode: mode, TTL: ttl, now: now}
}

func (c *Cache) clock() time.Time {
	if c == nil || c.now == nil {
		return time.Now()
	}
	return c.now()
}

func cacheTTLOf(c *Cache) CacheTTL {
	if c == nil {
		return DefaultCacheTTL()
	}
	return c.TTL
}

const cacheFileVersion = 1

type cacheEntry[T any] struct {
	FetchedAt int64 `json:"fetched_at"` // unix milliseconds
	Value     T     `json:"value"`
}

type cacheData[T any] struct {
	Version int                      `json:"version"`
	Entries map[string]cacheEntry[T] `json:"entries"`
}

// cacheSnapshot is an in-memory, load-once / save-once view of one category
// file for one workspace. Every operation is best-effort: a disabled or
// unreadable cache yields a usable snapshot whose gets always miss and whose
// save is a no-op, so callers never branch on "is caching on?".
type cacheSnapshot[T any] struct {
	cache    *Cache
	path     string
	ttlMS    int64
	data     *cacheData[T]
	validate func(key string, v T) bool
	changed  bool
}

// openCache loads (once) the category file for the workspace. category is the
// filename suffix; ttl is that category's freshness window; validate prunes
// corrupt entries on load (nil keeps every non-empty key).
func openCache[T any](c *Cache, category, workspaceURL string, ttl time.Duration, validate func(string, T) bool) *cacheSnapshot[T] {
	s := &cacheSnapshot[T]{
		cache:    c,
		ttlMS:    ttl.Milliseconds(),
		validate: validate,
		data:     &cacheData[T]{Version: cacheFileVersion, Entries: map[string]cacheEntry[T]{}},
	}
	if c == nil || c.Dir == "" || c.Mode == CacheOff {
		return s // disabled: usable, all gets miss, save no-ops
	}
	key := hashWorkspaceURL(workspaceURL)
	if key == "" {
		return s
	}
	s.path = filepath.Join(c.Dir, key+"-"+category+".json")

	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var data cacheData[T]
	if err := json.Unmarshal(raw, &data); err != nil || data.Version != cacheFileVersion || data.Entries == nil {
		return s
	}
	for k, e := range data.Entries {
		if k == "" || e.FetchedAt <= 0 || (validate != nil && !validate(k, e.Value)) {
			delete(data.Entries, k)
		}
	}
	s.data = &data
	return s
}

// get returns the cached value when present and within the category TTL.
// Refresh and Off modes always miss (Refresh then re-fetches and overwrites).
func (s *cacheSnapshot[T]) get(key string) (T, bool) {
	var zero T
	if s.path == "" || s.cache.Mode != CacheNormal || s.ttlMS <= 0 {
		return zero, false
	}
	e, ok := s.data.Entries[key]
	if !ok {
		return zero, false
	}
	if s.cache.clock().UnixMilli()-e.FetchedAt >= s.ttlMS {
		return zero, false
	}
	return e.Value, true
}

// set records a value for save() to persist. No-op when caching is disabled.
func (s *cacheSnapshot[T]) set(key string, v T) {
	if s.path == "" || key == "" {
		return
	}
	s.data.Entries[key] = cacheEntry[T]{FetchedAt: s.cache.clock().UnixMilli(), Value: v}
	s.changed = true
}

// save prunes expired entries and writes the file once, best-effort.
func (s *cacheSnapshot[T]) save() {
	if s.path == "" {
		return
	}
	if s.pruneExpired() {
		s.changed = true
	}
	if !s.changed {
		return
	}
	raw, err := json.Marshal(s.data)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(s.path, raw, 0o600)
}

func (s *cacheSnapshot[T]) pruneExpired() bool {
	if s.ttlMS <= 0 {
		return false
	}
	now := s.cache.clock().UnixMilli()
	changed := false
	for _, k := range slices.Sorted(maps.Keys(s.data.Entries)) {
		if now-s.data.Entries[k].FetchedAt >= s.ttlMS {
			delete(s.data.Entries, k)
			changed = true
		}
	}
	return changed
}

// hashWorkspaceURL reduces a workspace URL to a stable 16-hex filename key
// (host only, lowercased) — one cache file per workspace, no URL in the name.
// Returns "" when no host can be derived (caching then disables).
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
