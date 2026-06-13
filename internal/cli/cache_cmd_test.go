package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shhac/agent-slack/internal/slack"
)

// seedCache writes a downloads file and the acme workspace's cache dir under
// the fixture's appCacheDir, returning the two paths.
func seedCache(t *testing.T) (downloadsFile, wsDir string) {
	t.Helper()
	downloadsFile = filepath.Join(downloadsDir(), "F0FILE.txt")
	if err := os.MkdirAll(filepath.Dir(downloadsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(downloadsFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	wsDir = filepath.Join(appCacheDir(), slack.WorkspaceCacheKey("https://acme.slack.com"))
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "channels.json"), []byte(`{"version":1,"entries":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return downloadsFile, wsDir
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestCachePurgeDownloadsOnly(t *testing.T) {
	f := newCLIFixture(t) // sets XDG_CACHE_HOME + acme workspace
	dl, wsDir := seedCache(t)

	if _, _, err := f.run(t, "cache", "purge", "--downloads"); err != nil {
		t.Fatal(err)
	}
	if exists(dl) {
		t.Error("--downloads should clear the downloads file")
	}
	if !exists(wsDir) {
		t.Error("--downloads alone must NOT touch the resolution cache")
	}
}

func TestCachePurgeWorkspaceLeavesDownloads(t *testing.T) {
	f := newCLIFixture(t)
	dl, wsDir := seedCache(t)

	if _, _, err := f.run(t, "cache", "purge", "--workspace", "acme"); err != nil {
		t.Fatal(err)
	}
	if exists(wsDir) {
		t.Error("workspace purge should clear that workspace's cache")
	}
	if !exists(dl) {
		t.Error("workspace purge must leave downloads (they aren't workspace-scoped)")
	}
}

func TestCachePurgeWorkspaceAndDownloads(t *testing.T) {
	f := newCLIFixture(t)
	dl, wsDir := seedCache(t)

	if _, _, err := f.run(t, "cache", "purge", "--workspace", "acme", "--downloads"); err != nil {
		t.Fatal(err)
	}
	if exists(wsDir) || exists(dl) {
		t.Error("--workspace + --downloads should clear both")
	}
}
