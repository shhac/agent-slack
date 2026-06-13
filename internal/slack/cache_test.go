package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testWS = "https://acme.slack.com"

func testCache(dir string, mode CacheMode, now time.Time) *Cache {
	return NewCache(dir, mode, DefaultCacheTTL(), func() time.Time { return now })
}

func wsCachePath(dir, workspaceURL, category string) string {
	return filepath.Join(dir, hashWorkspaceURL(workspaceURL), category+".json")
}

func TestCacheSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := testCache(dir, CacheNormal, now)

	snap := openCache[string](c, "things", testWS, time.Hour, nil)
	if _, ok := snap.get("k"); ok {
		t.Fatal("empty cache should miss")
	}
	snap.set("k", "v")
	snap.save()

	if _, err := os.Stat(wsCachePath(dir, testWS, "things")); err != nil {
		t.Fatalf("save should create the per-workspace dir + file: %v", err)
	}
	again := openCache[string](c, "things", testWS, time.Hour, nil)
	if v, ok := again.get("k"); !ok || v != "v" {
		t.Errorf("got (%q,%v)", v, ok)
	}
}

func TestCacheSnapshotTTLBoundary(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	seed := testCache(dir, CacheNormal, now)
	snap := openCache[string](seed, "things", testWS, time.Hour, nil)
	snap.set("k", "v")
	snap.save()

	// One millisecond before expiry: hit. Exactly at expiry: miss (>= cutoff).
	justBefore := testCache(dir, CacheNormal, now.Add(time.Hour-time.Millisecond))
	if _, ok := openCache[string](justBefore, "things", testWS, time.Hour, nil).get("k"); !ok {
		t.Error("entry just inside the TTL should hit")
	}
	atExpiry := testCache(dir, CacheNormal, now.Add(time.Hour))
	if _, ok := openCache[string](atExpiry, "things", testWS, time.Hour, nil).get("k"); ok {
		t.Error("entry exactly at the TTL should miss")
	}
}

func TestCacheSnapshotModes(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	seed := testCache(dir, CacheNormal, now)
	s := openCache[string](seed, "things", testWS, time.Hour, nil)
	s.set("k", "v1")
	s.save()

	// Refresh mode: reads miss, writes land.
	refresh := testCache(dir, CacheRefresh, now)
	rs := openCache[string](refresh, "things", testWS, time.Hour, nil)
	if _, ok := rs.get("k"); ok {
		t.Error("refresh mode must skip reads")
	}
	rs.set("k", "v2")
	rs.save()
	normal := testCache(dir, CacheNormal, now)
	if v, _ := openCache[string](normal, "things", testWS, time.Hour, nil).get("k"); v != "v2" {
		t.Errorf("refresh write not persisted: %q", v)
	}

	// Off mode: reads miss AND writes are dropped.
	off := testCache(dir, CacheOff, now)
	os_ := openCache[string](off, "things", testWS, time.Hour, nil)
	if _, ok := os_.get("k"); ok {
		t.Error("off mode must not read")
	}
	os_.set("k", "v3")
	os_.save()
	if v, _ := openCache[string](normal, "things", testWS, time.Hour, nil).get("k"); v != "v2" {
		t.Errorf("off mode must not write; got %q", v)
	}

	// Zero TTL: reads always miss, writes still land.
	zero := openCache[string](testCache(dir, CacheNormal, now), "things", testWS, 0, nil)
	if _, ok := zero.get("k"); ok {
		t.Error("zero TTL must disable reads")
	}
}

func TestCacheSnapshotCorruptAndMismatchedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := testCache(dir, CacheNormal, now)
	path := wsCachePath(dir, testWS, "things")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"not json":      `{{{`,
		"wrong version": `{"version":99,"entries":{"k":{"fetched_at":1,"value":"v"}}}`,
		"null entries":  `{"version":1,"entries":null}`,
	}
	for name, raw := range cases {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		snap := openCache[string](c, "things", testWS, time.Hour, nil)
		if _, ok := snap.get("k"); ok {
			t.Errorf("%s: corrupt file must read as empty", name)
		}
		// And the snapshot stays usable: set+save round-trips.
		snap.set("k2", "v2")
		snap.save()
		if v, ok := openCache[string](c, "things", testWS, time.Hour, nil).get("k2"); !ok || v != "v2" {
			t.Errorf("%s: snapshot not usable after corrupt load: (%q,%v)", name, v, ok)
		}
	}
}

func TestCacheSnapshotValidatorPrunesOnLoad(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := testCache(dir, CacheNormal, now)

	raw, _ := json.Marshal(cacheData[string]{Version: cacheFileVersion, Entries: map[string]cacheEntry[string]{
		"good":    {FetchedAt: now.UnixMilli(), Value: "keep"},
		"bad":     {FetchedAt: now.UnixMilli(), Value: "drop-me"},
		"no-time": {FetchedAt: 0, Value: "keep"},
		"":        {FetchedAt: now.UnixMilli(), Value: "keep"},
	}})
	path := wsCachePath(dir, testWS, "things")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	snap := openCache[string](c, "things", testWS, time.Hour,
		func(_ string, v string) bool { return !strings.HasPrefix(v, "drop") })
	if _, ok := snap.get("good"); !ok {
		t.Error("valid entry dropped")
	}
	for _, k := range []string{"bad", "no-time", ""} {
		if _, ok := snap.get(k); ok {
			t.Errorf("invalid entry %q survived the load validator", k)
		}
	}
}

func TestCacheSnapshotPruneExpiredOnSave(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	seed := testCache(dir, CacheNormal, now)
	s := openCache[string](seed, "things", testWS, time.Hour, nil)
	s.set("old", "v")
	s.save()

	// Two hours later a write triggers a save, which prunes the stale entry
	// from the FILE (not just the TTL check on read).
	later := testCache(dir, CacheNormal, now.Add(2*time.Hour))
	ls := openCache[string](later, "things", testWS, time.Hour, nil)
	ls.set("new", "v")
	ls.save()

	raw, err := os.ReadFile(wsCachePath(dir, testWS, "things"))
	if err != nil {
		t.Fatal(err)
	}
	var data cacheData[string]
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if _, exists := data.Entries["old"]; exists {
		t.Error("expired entry not pruned from the file on save")
	}
	if _, exists := data.Entries["new"]; !exists {
		t.Error("fresh entry missing after prune")
	}
}

func TestCacheSnapshotSetIgnoresEmptyKeyAndDisabled(t *testing.T) {
	// Empty key on an enabled cache: ignored, no write.
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	s := openCache[string](testCache(dir, CacheNormal, now), "things", testWS, time.Hour, nil)
	s.set("", "v")
	s.save()
	if _, err := os.Stat(wsCachePath(dir, testWS, "things")); !os.IsNotExist(err) {
		t.Error("empty-key set must not create a file")
	}

	// Nil cache / empty dir / no workspace: snapshot is inert but usable.
	for name, snap := range map[string]*cacheSnapshot[string]{
		"nil cache":    openCache[string](nil, "things", testWS, time.Hour, nil),
		"empty dir":    openCache[string](testCache("", CacheNormal, now), "things", testWS, time.Hour, nil),
		"no workspace": openCache[string](testCache(dir, CacheNormal, now), "things", "", time.Hour, nil),
	} {
		snap.set("k", "v")
		snap.save() // must not panic or write
		if _, ok := snap.get("k"); ok {
			t.Errorf("%s: inert snapshot should never hit", name)
		}
	}
}

func TestHashWorkspaceURL(t *testing.T) {
	if hashWorkspaceURL("") != "" || hashWorkspaceURL("   ") != "" {
		t.Error("empty/whitespace input must yield no key (disables caching)")
	}
	a := hashWorkspaceURL("https://acme.slack.com")
	if a == "" || len(a) != 16 {
		t.Fatalf("hash = %q, want 16 hex chars", a)
	}
	// Deterministic, host-derived, case-insensitive: trailing slash and case
	// variations of the same host collapse to one key.
	for _, variant := range []string{"https://acme.slack.com/", "https://ACME.slack.com", "  https://acme.slack.com  "} {
		if got := hashWorkspaceURL(variant); got != a {
			t.Errorf("hash(%q) = %q, want %q", variant, got, a)
		}
	}
	if hashWorkspaceURL("https://other.slack.com") == a {
		t.Error("different hosts must not collide")
	}
	// A non-URL string still hashes (full-string fallback).
	if hashWorkspaceURL("not a url") == "" {
		t.Error("non-URL input should fall back to hashing the string")
	}
}
