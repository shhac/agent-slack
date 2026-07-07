package auth

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang/snappy"
	_ "modernc.org/sqlite"
)

// writeGeckoLocalStorage builds a Firefox localStorage data.sqlite holding a
// localConfig row under the given storage origin, with the value optionally
// Snappy-compressed the way Firefox stores larger entries.
func writeGeckoLocalStorage(t *testing.T, profile, origin string, compress bool) string {
	t.Helper()
	lsDir := filepath.Join(profile, "storage", "default", origin, "ls")
	if err := os.MkdirAll(lsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(lsDir, "data.sqlite")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE data (key TEXT, value BLOB, compression_type INTEGER)`); err != nil {
		t.Fatal(err)
	}

	value := []byte(sampleConfig)
	compType := 0
	if compress {
		value = snappy.Encode(nil, []byte(sampleConfig))
		compType = 1
	}
	if _, err := db.Exec(`INSERT INTO data (key, value, compression_type) VALUES ('localConfig_v3', ?, ?)`,
		value, compType); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// TestFirefoxTeamsFromContainerSnappy is the regression for real Zen/Firefox
// setups: Slack in a container origin (^userContextId) with a Snappy-compressed
// localConfig. sampleConfig has two xoxc teams (T1, T2) plus a non-browser xoxb
// (T3) that must be filtered out.
func TestFirefoxTeamsFromContainerSnappy(t *testing.T) {
	profile := t.TempDir()
	dbPath := writeGeckoLocalStorage(t, profile, "https+++app.slack.com^userContextId=2", true)

	teams, lsPath, ok := firefoxTeamsFromProfile(profile)
	if !ok {
		t.Fatal("expected teams from the container-scoped, snappy-compressed store")
	}
	if len(teams) != 2 {
		t.Errorf("teams = %d, want 2 (xoxc only)", len(teams))
	}
	if lsPath != dbPath {
		t.Errorf("lsPath = %q, want %q", lsPath, dbPath)
	}
}

// TestFirefoxTeamsUncompressedPlainOrigin covers the non-container, non-compressed
// case (compression_type 0) so the raw-value path stays exercised.
func TestFirefoxTeamsUncompressedPlainOrigin(t *testing.T) {
	profile := t.TempDir()
	writeGeckoLocalStorage(t, profile, "https+++app.slack.com", false)

	teams, _, ok := firefoxTeamsFromProfile(profile)
	if !ok || len(teams) != 2 {
		t.Errorf("plain origin: ok=%v teams=%d, want ok=true teams=2", ok, len(teams))
	}
}

func TestFirefoxTeamsNoSlackOrigin(t *testing.T) {
	profile := t.TempDir()
	writeGeckoLocalStorage(t, profile, "https+++example.com", true)

	if _, _, ok := firefoxTeamsFromProfile(profile); ok {
		t.Error("expected no teams when no app.slack.com origin exists")
	}
}
