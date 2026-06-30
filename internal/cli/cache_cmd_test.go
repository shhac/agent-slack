package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// seedCache writes a downloaded file and a resolution-cache category file under
// the acme identity's subtree (downloads nest inside it now), returning both
// paths.
func seedCache(t *testing.T) (downloadsFile, categoryFile string) {
	t.Helper()
	key := fixtureCacheKey()
	downloadsFile = filepath.Join(downloadsDir(key), "F0FILE.txt")
	if err := os.MkdirAll(filepath.Dir(downloadsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(downloadsFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	categoryFile = filepath.Join(appCacheDir(), key, "channels.json")
	if err := os.MkdirAll(filepath.Dir(categoryFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(categoryFile, []byte(`{"version":1,"entries":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return downloadsFile, categoryFile
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestCachePurgeDownloadsOnly(t *testing.T) {
	f := newCLIFixture(t) // sets XDG_CACHE_HOME + acme workspace
	dl, cat := seedCache(t)

	if _, _, err := f.run(t, "cache", "purge", "--downloads"); err != nil {
		t.Fatal(err)
	}
	if exists(dl) {
		t.Error("--downloads should clear the downloads file")
	}
	if !exists(cat) {
		t.Error("--downloads alone must NOT touch the resolution cache")
	}
}

func TestCachePurgeWorkspaceLeavesDownloads(t *testing.T) {
	f := newCLIFixture(t)
	dl, cat := seedCache(t)

	if _, _, err := f.run(t, "cache", "purge", "--workspace", "acme"); err != nil {
		t.Fatal(err)
	}
	if exists(cat) {
		t.Error("workspace purge should clear that identity's resolution cache")
	}
	if !exists(dl) {
		t.Error("workspace purge must leave downloads (cleared only by --downloads)")
	}
}

func TestCachePurgeWorkspaceAndDownloads(t *testing.T) {
	f := newCLIFixture(t)
	dl, cat := seedCache(t)

	if _, _, err := f.run(t, "cache", "purge", "--workspace", "acme", "--downloads"); err != nil {
		t.Fatal(err)
	}
	if exists(cat) || exists(dl) {
		t.Error("--workspace + --downloads should clear both")
	}
}

func TestAuthRemovePurgesIdentityCache(t *testing.T) {
	f := newCLIFixture(t)
	dl, cat := seedCache(t) // both under the acme identity subtree
	idDir := filepath.Join(appCacheDir(), fixtureCacheKey())
	if !exists(idDir) {
		t.Fatal("precondition: identity cache dir should be seeded")
	}

	if _, _, err := f.run(t, "auth", "remove", "https://acme.slack.com"); err != nil {
		t.Fatal(err)
	}
	if exists(idDir) || exists(dl) || exists(cat) {
		t.Error("auth remove must clear the whole identity cache subtree (cache + downloads)")
	}
}
