package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testWS  = "https://acme.slack.com"
	testKey = "T_ACME/U_PAUL" // the <team_id>/<user_id> identity namespace under test
)

func testCache(dir string, mode CacheMode, now time.Time) *Cache {
	return NewCache(dir, testKey, mode, DefaultCacheTTL(), func() time.Time { return now })
}

func wsCachePath(dir, key, category string) string {
	return filepath.Join(dir, key, category+".json")
}

func TestCacheSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := testCache(dir, CacheNormal, now)

	snap := openCache[string](c, "things", time.Hour, nil)
	if _, ok := snap.get("k"); ok {
		t.Fatal("empty cache should miss")
	}
	snap.set("k", "v")
	snap.save()

	if _, err := os.Stat(wsCachePath(dir, testKey, "things")); err != nil {
		t.Fatalf("save should create the per-workspace dir + file: %v", err)
	}
	again := openCache[string](c, "things", time.Hour, nil)
	if v, ok := again.get("k"); !ok || v != "v" {
		t.Errorf("got (%q,%v)", v, ok)
	}
}

func TestCacheSnapshotTTLBoundary(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	seed := testCache(dir, CacheNormal, now)
	snap := openCache[string](seed, "things", time.Hour, nil)
	snap.set("k", "v")
	snap.save()

	// One millisecond before expiry: hit. Exactly at expiry: miss (>= cutoff).
	justBefore := testCache(dir, CacheNormal, now.Add(time.Hour-time.Millisecond))
	if _, ok := openCache[string](justBefore, "things", time.Hour, nil).get("k"); !ok {
		t.Error("entry just inside the TTL should hit")
	}
	atExpiry := testCache(dir, CacheNormal, now.Add(time.Hour))
	if _, ok := openCache[string](atExpiry, "things", time.Hour, nil).get("k"); ok {
		t.Error("entry exactly at the TTL should miss")
	}
}

func TestCacheSnapshotModes(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	seed := testCache(dir, CacheNormal, now)
	s := openCache[string](seed, "things", time.Hour, nil)
	s.set("k", "v1")
	s.save()

	// Refresh mode: reads miss, writes land.
	refresh := testCache(dir, CacheRefresh, now)
	rs := openCache[string](refresh, "things", time.Hour, nil)
	if _, ok := rs.get("k"); ok {
		t.Error("refresh mode must skip reads")
	}
	rs.set("k", "v2")
	rs.save()
	normal := testCache(dir, CacheNormal, now)
	if v, _ := openCache[string](normal, "things", time.Hour, nil).get("k"); v != "v2" {
		t.Errorf("refresh write not persisted: %q", v)
	}

	// Off mode: reads miss AND writes are dropped.
	off := testCache(dir, CacheOff, now)
	os_ := openCache[string](off, "things", time.Hour, nil)
	if _, ok := os_.get("k"); ok {
		t.Error("off mode must not read")
	}
	os_.set("k", "v3")
	os_.save()
	if v, _ := openCache[string](normal, "things", time.Hour, nil).get("k"); v != "v2" {
		t.Errorf("off mode must not write; got %q", v)
	}

	// Zero TTL: reads always miss, writes still land.
	zero := openCache[string](testCache(dir, CacheNormal, now), "things", 0, nil)
	if _, ok := zero.get("k"); ok {
		t.Error("zero TTL must disable reads")
	}
}

func TestCacheSnapshotCorruptAndMismatchedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := testCache(dir, CacheNormal, now)
	path := wsCachePath(dir, testKey, "things")
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
		snap := openCache[string](c, "things", time.Hour, nil)
		if _, ok := snap.get("k"); ok {
			t.Errorf("%s: corrupt file must read as empty", name)
		}
		// And the snapshot stays usable: set+save round-trips.
		snap.set("k2", "v2")
		snap.save()
		if v, ok := openCache[string](c, "things", time.Hour, nil).get("k2"); !ok || v != "v2" {
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
	path := wsCachePath(dir, testKey, "things")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	snap := openCache[string](c, "things", time.Hour,
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
	s := openCache[string](seed, "things", time.Hour, nil)
	s.set("old", "v")
	s.save()

	// Two hours later a write triggers a save, which prunes the stale entry
	// from the FILE (not just the TTL check on read).
	later := testCache(dir, CacheNormal, now.Add(2*time.Hour))
	ls := openCache[string](later, "things", time.Hour, nil)
	ls.set("new", "v")
	ls.save()

	raw, err := os.ReadFile(wsCachePath(dir, testKey, "things"))
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
	s := openCache[string](testCache(dir, CacheNormal, now), "things", time.Hour, nil)
	s.set("", "v")
	s.save()
	if _, err := os.Stat(wsCachePath(dir, testKey, "things")); !os.IsNotExist(err) {
		t.Error("empty-key set must not create a file")
	}

	// Nil cache / empty dir / no identity key: snapshot is inert but usable.
	noKey := NewCache(dir, "", CacheNormal, DefaultCacheTTL(), func() time.Time { return now })
	for name, snap := range map[string]*cacheSnapshot[string]{
		"nil cache":       openCache[string](nil, "things", time.Hour, nil),
		"empty dir":       openCache[string](testCache("", CacheNormal, now), "things", time.Hour, nil),
		"no identity key": openCache[string](noKey, "things", time.Hour, nil),
	} {
		snap.set("k", "v")
		snap.save() // must not panic or write
		if _, ok := snap.get("k"); ok {
			t.Errorf("%s: inert snapshot should never hit", name)
		}
	}
}

func TestIdentityCacheKey(t *testing.T) {
	if got := IdentityCacheKey("T123", "U456"); got != filepath.Join("T123", "U456") {
		t.Errorf("key = %q, want T123/U456", got)
	}
	// A missing half disables caching (empty key), so data is never scoped to a
	// partial identity.
	for _, c := range []struct{ team, user string }{{"", "U456"}, {"T123", ""}, {"", ""}, {"  ", "U456"}} {
		if got := IdentityCacheKey(c.team, c.user); got != "" {
			t.Errorf("IdentityCacheKey(%q,%q) = %q, want empty", c.team, c.user, got)
		}
	}
	// Path-traversal forms can never escape the cache root: a bare ".."/"."
	// segment is rejected, and embedded separators are replaced so each id stays
	// a single directory level (exactly one separator — the team/user join).
	if got := IdentityCacheKey("..", "U456"); got != "" {
		t.Errorf("traversal team yielded %q, want empty", got)
	}
	if got := IdentityCacheKey("T/../x", "U/y"); strings.Count(got, string(filepath.Separator)) != 1 {
		t.Errorf("embedded separators not sanitised to a single level: %q", got)
	}
}
