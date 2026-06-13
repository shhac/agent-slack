package slack

import "testing"

func TestCacheAdminInspectAndPurge(t *testing.T) {
	dir := t.TempDir()
	ws := "https://acme.slack.com"
	other := "https://globex.slack.com"

	writeCacheCategory(t, dir, ws, "channels", map[string]cacheEntry[CompactChannel]{
		"C0AAAA1111": {FetchedAt: 1000, Value: CompactChannel{ID: "C0AAAA1111", Name: "devs"}},
		"C0BBBB2222": {FetchedAt: 3000, Value: CompactChannel{ID: "C0BBBB2222", Name: "design"}},
	})
	writeCacheCategory(t, dir, other, "users", map[string]cacheEntry[CompactUser]{
		"U0AAAA1111": {FetchedAt: 2000, Value: CompactUser{ID: "U0AAAA1111"}},
	})

	keys, err := CachedWorkspaceKeys(dir)
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys = %v, err %v", keys, err)
	}

	cats, err := InspectCacheDir(dir, WorkspaceCacheKey(ws))
	if err != nil || len(cats) != 1 || cats[0].Category != "channels" || cats[0].Entries != 2 {
		t.Fatalf("inspect = %+v, err %v", cats, err)
	}
	if cats[0].OldestMS != 1000 || cats[0].NewestMS != 3000 || cats[0].Bytes == 0 {
		t.Errorf("stat = %+v", cats[0])
	}

	// Purge one workspace; the other survives.
	if err := PurgeCacheDir(dir, WorkspaceCacheKey(ws)); err != nil {
		t.Fatal(err)
	}
	if cats, _ := InspectCacheDir(dir, WorkspaceCacheKey(ws)); len(cats) != 0 {
		t.Errorf("purged workspace still has %d categories", len(cats))
	}
	if keys, _ := CachedWorkspaceKeys(dir); len(keys) != 1 {
		t.Errorf("other workspace should remain: %v", keys)
	}

	// Purge all clears the rest.
	if _, err := PurgeAllCaches(dir); err != nil {
		t.Fatal(err)
	}
	if keys, _ := CachedWorkspaceKeys(dir); len(keys) != 0 {
		t.Errorf("purge-all left %v", keys)
	}
}

func TestCacheAdminEmptyDir(t *testing.T) {
	// A never-used cache dir is not an error.
	keys, err := CachedWorkspaceKeys(t.TempDir())
	if err != nil || len(keys) != 0 {
		t.Errorf("keys=%v err=%v", keys, err)
	}
	if _, err := InspectCacheDir(t.TempDir(), "deadbeef"); err != nil {
		t.Errorf("inspect missing = %v", err)
	}
}
