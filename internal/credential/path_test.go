package credential

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The agent-* family stores config under $XDG_CONFIG_HOME → ~/.config on
// every platform (never macOS's Application Support). agent-slack uses
// app.paulie.agent-slack rather than the plain tool name because the TS
// stablyai-agent-slack already owns ~/.config/agent-slack/credentials.json.
func TestDefaultPathAvoidsTSToolDir(t *testing.T) {
	t.Setenv("AGENT_SLACK_CREDENTIALS", "")

	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	got, err := defaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/tmp/xdg-test", "app.paulie.agent-slack", "credentials.json") {
		t.Errorf("XDG path = %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	got, err = defaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(got)) != "app.paulie.agent-slack" ||
		filepath.Base(filepath.Dir(filepath.Dir(got))) != ".config" {
		t.Errorf("fallback path = %q, want ~/.config/app.paulie.agent-slack/credentials.json", got)
	}

	t.Setenv("AGENT_SLACK_CREDENTIALS", "/custom/creds.json")
	got, err = defaultPath()
	if err != nil || got != "/custom/creds.json" {
		t.Errorf("env override = %q, %v", got, err)
	}
}

func TestMigrateLegacyFile(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	legacy := filepath.Join(base, "agent-slack", "credentials.json")
	ours := filepath.Join(base, "app.paulie.agent-slack", "credentials.json")

	legacyContent := `{"version":1,"workspaces":[{"workspace_url":"https://acme.slack.com","auth":{"auth_type":"browser","xoxc_token":"__KEYCHAIN__","xoxd_cookie":"__KEYCHAIN__"}}]}`
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(legacyContent), 0o600); err != nil {
		t.Fatal(err)
	}

	migrateLegacyFile(ours)

	raw, err := os.ReadFile(ours)
	if err != nil {
		t.Fatalf("migration did not create %s: %v", ours, err)
	}
	var creds Credentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		t.Fatal(err)
	}
	if creds.Version != 1 || len(creds.Workspaces) != 1 || creds.Workspaces[0].URL != "https://acme.slack.com" {
		t.Errorf("migrated creds = %+v", creds)
	}
	// The TS tool's file is read, never written.
	after, _ := os.ReadFile(legacy)
	if string(after) != legacyContent {
		t.Error("legacy file must not be modified")
	}

	// A second run must not clobber our file.
	if err := os.WriteFile(ours, []byte(`{"version":1,"workspaces":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	migrateLegacyFile(ours)
	raw, _ = os.ReadFile(ours)
	if string(raw) != `{"version":1,"workspaces":[]}` {
		t.Error("existing store must not be overwritten by migration")
	}
}

// Every config-shaped file this tool writes carries the current schema
// version, regardless of what the caller passed in.
func TestSaveAlwaysWritesCurrentVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := NewWithStore(path, NewMemoryKeychain())
	if err := store.Save(&Credentials{Workspaces: []Workspace{}}); err != nil { // Version deliberately zero
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk["version"] != float64(storeVersion) {
		t.Errorf("version = %v, want %d", onDisk["version"], storeVersion)
	}
}
