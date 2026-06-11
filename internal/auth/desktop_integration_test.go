package auth

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
)

// TestReadSlackLocalConfigFromLevelDB builds a real LevelDB the way Chromium
// Local Storage stores it (a key containing "localConfig_v2") and verifies the
// reader recovers the value and the team parser extracts the xoxc workspaces.
func TestReadSlackLocalConfigFromLevelDB(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leveldb")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := append([]byte("_https://app.slack.com\x00\x01"), []byte("localConfig_v2")...)
	if err := db.Put(key, []byte(sampleConfig), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := readSlackLocalConfig(dir)
	if err != nil {
		t.Fatalf("readSlackLocalConfig: %v", err)
	}
	cfg, err := parseLocalConfig(raw)
	if err != nil {
		t.Fatalf("parseLocalConfig: %v", err)
	}
	teams := teamsFromLocalConfig(cfg)
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams, got %d: %+v", len(teams), teams)
	}
}

// TestExtractChromiumCookieDPlaintext builds a Cookies SQLite DB with an
// unencrypted xoxd value (Chromium sometimes stores it in `value`) and verifies
// the reader returns it without needing the Safe Storage password.
func TestExtractChromiumCookieDPlaintext(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "Cookies")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB);
		INSERT INTO cookies VALUES ('.slack.com', 'd', 'xoxd-plaincookie', x'');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractChromiumCookieD(dbPath, nil)
	if err != nil {
		t.Fatalf("extractChromiumCookieD: %v", err)
	}
	if got != "xoxd-plaincookie" {
		t.Errorf("cookie = %q, want xoxd-plaincookie", got)
	}
}
