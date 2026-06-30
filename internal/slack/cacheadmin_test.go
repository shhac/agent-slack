package slack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheAdminInspectAndPurge(t *testing.T) {
	dir := t.TempDir()
	// Two users on the SAME team plus a third on another team — exercises the
	// two-level <team>/<user> walk and the team-prune on the last user's purge.
	acmePaul := IdentityCacheKey("T_ACME", "U_PAUL")
	acmeSue := IdentityCacheKey("T_ACME", "U_SUE")
	other := IdentityCacheKey("T_GLOBEX", "U_PAUL")

	writeCacheCategory(t, dir, acmePaul, "channels", map[string]cacheEntry[CompactChannel]{
		"C0AAAA1111": {FetchedAt: 1000, Value: CompactChannel{ID: "C0AAAA1111", Name: "devs"}},
		"C0BBBB2222": {FetchedAt: 3000, Value: CompactChannel{ID: "C0BBBB2222", Name: "design"}},
	})
	writeCacheCategory(t, dir, acmeSue, "users", map[string]cacheEntry[CompactUser]{
		"U0AAAA1111": {FetchedAt: 2000, Value: CompactUser{ID: "U0AAAA1111"}},
	})
	writeCacheCategory(t, dir, other, "users", map[string]cacheEntry[CompactUser]{
		"U0AAAA1111": {FetchedAt: 2000, Value: CompactUser{ID: "U0AAAA1111"}},
	})

	keys, err := CachedIdentityKeys(dir)
	if err != nil || len(keys) != 3 {
		t.Fatalf("keys = %v, err %v", keys, err)
	}

	cats, err := InspectCacheDir(dir, acmePaul)
	if err != nil || len(cats) != 1 || cats[0].Category != "channels" || cats[0].Entries != 2 {
		t.Fatalf("inspect = %+v, err %v", cats, err)
	}
	if cats[0].OldestMS != 1000 || cats[0].NewestMS != 3000 || cats[0].Bytes == 0 {
		t.Errorf("stat = %+v", cats[0])
	}

	// Purge one identity; the sibling on the same team survives (team dir kept).
	if err := PurgeCacheDir(dir, acmePaul); err != nil {
		t.Fatal(err)
	}
	if cats, _ := InspectCacheDir(dir, acmePaul); len(cats) != 0 {
		t.Errorf("purged identity still has %d categories", len(cats))
	}
	if keys, _ := CachedIdentityKeys(dir); len(keys) != 2 {
		t.Errorf("two identities should remain: %v", keys)
	}

	// Purge all clears the rest, leaving no team dirs behind.
	if _, err := PurgeAllCaches(dir); err != nil {
		t.Fatal(err)
	}
	if keys, _ := CachedIdentityKeys(dir); len(keys) != 0 {
		t.Errorf("purge-all left %v", keys)
	}
}

func TestPurgeCacheDirKeepsDownloadsButIdentityDirRemovesAll(t *testing.T) {
	dir := t.TempDir()
	key := IdentityCacheKey("T_ACME", "U_PAUL")
	writeCacheCategory(t, dir, key, "channels", map[string]cacheEntry[CompactChannel]{
		"C1": {FetchedAt: 1000, Value: CompactChannel{ID: "C1", Name: "devs"}},
	})
	dl := filepath.Join(dir, key, DownloadsSubdir, "F0FILE.txt")
	if err := os.MkdirAll(filepath.Dir(dl), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dl, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A resolution-cache purge clears categories but preserves downloads.
	if err := PurgeCacheDir(dir, key); err != nil {
		t.Fatal(err)
	}
	if cats, _ := InspectCacheDir(dir, key); len(cats) != 0 {
		t.Errorf("resolution cache not purged: %+v", cats)
	}
	if _, err := os.Stat(dl); err != nil {
		t.Errorf("downloads must survive a resolution-cache purge: %v", err)
	}

	// A full identity purge removes everything, including downloads, and prunes
	// the now-empty team dir.
	if err := PurgeIdentityDir(dir, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, key)); !os.IsNotExist(err) {
		t.Errorf("identity dir should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "T_ACME")); !os.IsNotExist(err) {
		t.Errorf("empty team dir should be pruned: %v", err)
	}
}

func TestPurgeAllClearsLegacyArtifacts(t *testing.T) {
	dir := t.TempDir()
	// Legacy orphans: a 16-hex host-hash resolution dir, the old flat downloads/
	// and emoji-images/, and the defunct sentinel.
	legacy := filepath.Join(dir, "0123456789abcdef")
	for _, p := range []string{legacy, filepath.Join(dir, "downloads"), filepath.Join(dir, "emoji-images")} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".layout-v2"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A current identity whose downloads must be kept by a (downloads-preserving)
	// purge-all.
	key := IdentityCacheKey("T_ACME", "U_PAUL")
	writeCacheCategory(t, dir, key, "channels", map[string]cacheEntry[CompactChannel]{
		"C1": {FetchedAt: 1000, Value: CompactChannel{ID: "C1", Name: "devs"}},
	})
	keepDownload := filepath.Join(dir, key, DownloadsSubdir, "F0FILE.txt")
	if err := os.MkdirAll(filepath.Dir(keepDownload), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keepDownload, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := PurgeAllCaches(dir); err != nil {
		t.Fatal(err)
	}

	for _, gone := range []string{legacy, filepath.Join(dir, "downloads"), filepath.Join(dir, "emoji-images"), filepath.Join(dir, ".layout-v2")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("legacy orphan %s should be removed by purge-all: %v", gone, err)
		}
	}
	// The current identity's resolution cache is cleared, but its downloads stay.
	if cats, _ := InspectCacheDir(dir, key); len(cats) != 0 {
		t.Errorf("current identity resolution cache should be purged: %+v", cats)
	}
	if _, err := os.Stat(keepDownload); err != nil {
		t.Errorf("current identity's downloads must survive purge-all: %v", err)
	}
}

func TestIsLegacyArtifactName(t *testing.T) {
	legacy := []string{"0123456789abcdef", "fedcba9876543210", DownloadsSubdir, EmojiImagesSubdir}
	for _, n := range legacy {
		if !isLegacyArtifactName(n) {
			t.Errorf("%q should be classified legacy", n)
		}
	}
	// Current-layout team dirs (uppercase Slack ids) and anything that isn't an
	// exact 16-lowercase-hex host hash must never be swept.
	current := []string{"T0123456789ABCDE", "T_ACME", "E01ABCDE", "0123456789abcde" /*15*/, "0123456789abcdef0" /*17*/, "0123456789ABCDEF" /*upper*/, "users", "channels.json"}
	for _, n := range current {
		if isLegacyArtifactName(n) {
			t.Errorf("%q must NOT be classified legacy (false positive)", n)
		}
	}
}

func TestCacheAdminEmptyDir(t *testing.T) {
	// A never-used cache dir is not an error.
	keys, err := CachedIdentityKeys(t.TempDir())
	if err != nil || len(keys) != 0 {
		t.Errorf("keys=%v err=%v", keys, err)
	}
	if _, err := InspectCacheDir(t.TempDir(), IdentityCacheKey("T1", "U1")); err != nil {
		t.Errorf("inspect missing = %v", err)
	}
}
