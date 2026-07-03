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

// TestReadSlackLocalConfigPicksHighestRank writes both a localConfig_v2 and a
// localConfig_v3 entry; the reader ranks by the key's trailing 8 bytes and must
// return the newer v3 value regardless of LevelDB iteration order.
func TestReadSlackLocalConfigPicksHighestRank(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leveldb")
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("_https://app.slack.com\x00\x01")
	keyFor := func(suffix string) []byte {
		return append(append([]byte{}, prefix...), []byte(suffix)...)
	}
	if err := db.Put(keyFor("localConfig_v2"), []byte("v2-old"), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Put(keyFor("localConfig_v3"), []byte("v3-new"), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := readSlackLocalConfig(dir)
	if err != nil {
		t.Fatalf("readSlackLocalConfig: %v", err)
	}
	if string(raw) != "v3-new" {
		t.Errorf("ranking chose %q, want the higher-ranked v3 value", raw)
	}
}

// TestExtractChromiumFromFiles exercises the shared file-based extractor that
// Slack Desktop and file-based browser sources (Opera) both use: LevelDB tokens
// + Cookies DB, returning a labelled Extracted. Asserts the source map keys are
// passed through unchanged (desktop relies on leveldb_path/cookies_path).
func TestExtractChromiumFromFiles(t *testing.T) {
	leveldbDir := filepath.Join(t.TempDir(), "leveldb")
	db, err := leveldb.OpenFile(leveldbDir, nil)
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

	cookiesDB := filepath.Join(t.TempDir(), "Cookies")
	cdb, err := sql.Open("sqlite", cookiesDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cdb.Exec(`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB);
		INSERT INTO cookies VALUES ('.slack.com', 'd', 'xoxd-fromfiles', x'');`); err != nil {
		t.Fatal(err)
	}
	if err := cdb.Close(); err != nil {
		t.Fatal(err)
	}

	source := map[string]string{"leveldb_path": leveldbDir, "cookies_path": cookiesDB}
	got, err := extractChromiumFromFiles(leveldbDir, cookiesDB, nil, source)
	if err != nil {
		t.Fatalf("extractChromiumFromFiles: %v", err)
	}
	if len(got.Teams) != 2 {
		t.Errorf("teams = %d, want 2", len(got.Teams))
	}
	if got.CookieD != "xoxd-fromfiles" {
		t.Errorf("cookie = %q, want xoxd-fromfiles", got.CookieD)
	}
	if got.Source["leveldb_path"] != leveldbDir || got.Source["cookies_path"] != cookiesDB {
		t.Errorf("source map keys not passed through: %+v", got.Source)
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
