package credential

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T, kc Keychain) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	return NewWithStore(path, kc)
}

// failingKeychain reports itself available but fails every Set — the macOS
// "keychain locked / security CLI errored" case. Secrets must then stay in the
// file rather than being replaced by an unrecoverable placeholder.
type failingKeychain struct{}

func (failingKeychain) Get(string) (string, bool) { return "", false }
func (failingKeychain) Set(string, string) bool   { return false }
func (failingKeychain) Delete(string)             {}
func (failingKeychain) Available() bool           { return true }

func TestSecretsStayInFileWhenKeychainSetFails(t *testing.T) {
	s := newTestStore(t, failingKeychain{})
	if _, err := s.Upsert(Workspace{
		URL:  "https://acme.slack.com",
		Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-secret", XOXD: "xoxd-secret"},
	}); err != nil {
		t.Fatal(err)
	}

	// Neither secret was persisted to the keychain, so both must remain in the
	// file — including xoxd, which previously got the placeholder unconditionally.
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "xoxc-secret") {
		t.Errorf("xoxc dropped when keychain Set failed:\n%s", raw)
	}
	if !strings.Contains(string(raw), "xoxd-secret") {
		t.Errorf("xoxd dropped when keychain Set failed:\n%s", raw)
	}
	if strings.Contains(string(raw), keychainPlaceholder) {
		t.Errorf("placeholder written for a secret the keychain never stored:\n%s", raw)
	}

	// A fresh load still recovers both secrets from the file.
	got, err := s.ResolveDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got.Auth.XOXC != "xoxc-secret" || got.Auth.XOXD != "xoxd-secret" {
		t.Errorf("secrets not recoverable after a failed keychain store: %+v", got.Auth)
	}
}

// TestStore_Headless_FileFallback exercises the real credential-WRITE path
// non-interactively, through the default keychain rather than an injected one.
// Setting the per-CLI keychain opt-out (derived by lib-agent-cli from the
// "app.paulie.agent-slack" service) makes creds.Keychain.Available() report
// false, so Save deterministically takes the 0600 file fallback on every
// platform — including darwin, where it would otherwise reach the `security`
// CLI and its GUI prompt. Using AGENT_SLACK_NO_KEYCHAIN (not the family-wide
// LIB_AGENT_NO_KEYCHAIN) also proves the lib's prefix derivation.
func TestStore_Headless_FileFallback(t *testing.T) {
	t.Setenv("AGENT_SLACK_NO_KEYCHAIN", "1")
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("AGENT_SLACK_CREDENTIALS", path)

	s, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Confirm we're going through the real default keychain and it's opted out.
	if s.kc.Available() {
		t.Fatal("keychain should report unavailable under AGENT_SLACK_NO_KEYCHAIN=1")
	}

	if _, err := s.Upsert(Workspace{
		URL:  "https://headless.slack.com",
		Name: "Headless",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-headless"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// The secret must have been written to the 0600 file, not a keychain
	// placeholder, because the keychain is opted out.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("credentials file not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("credentials mode=%o, want 0600", mode)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "xoxb-headless") {
		t.Errorf("token not written to file under keychain opt-out:\n%s", raw)
	}
	if strings.Contains(string(raw), keychainPlaceholder) {
		t.Errorf("keychain placeholder written despite opt-out:\n%s", raw)
	}

	// Round-trip via the read path.
	got, err := s.ResolveDefault()
	if err != nil {
		t.Fatalf("ResolveDefault: %v", err)
	}
	if got.Auth.Token != "xoxb-headless" {
		t.Errorf("token not round-tripped; got %q", got.Auth.Token)
	}

	// Remove and confirm it's gone.
	if err := s.Remove("https://headless.slack.com"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	creds, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(creds.Workspaces) != 0 {
		t.Errorf("workspace still present after Remove: %+v", creds.Workspaces)
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"https://acme.slack.com":                "https://acme.slack.com",
		"https://acme.slack.com/archives/C1/p2": "https://acme.slack.com",
		"https://acme.slack.com:443/?x=1#frag":  "https://acme.slack.com:443",
		"  https://Acme.slack.com/messages/  ":  "https://Acme.slack.com",
	}
	for in, want := range cases {
		got, err := normalizeURL(in)
		if err != nil || got != want {
			t.Errorf("normalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := normalizeURL("not-a-url"); err == nil {
		t.Error("expected error for non-URL")
	}
}

func TestUpsertAndResolveDefault(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	if _, err := s.Upsert(Workspace{
		URL:  "https://acme.slack.com/archives/C1",
		Name: "Acme",
		Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-abc", XOXD: "xoxd-zzz"},
	}); err != nil {
		t.Fatal(err)
	}

	def, err := s.ResolveDefault()
	if err != nil {
		t.Fatal(err)
	}
	if def.URL != "https://acme.slack.com" {
		t.Errorf("default URL = %q, want normalized https://acme.slack.com", def.URL)
	}
	if def.Auth.XOXC != "xoxc-abc" || def.Auth.XOXD != "xoxd-zzz" {
		t.Errorf("secrets not hydrated: %+v", def.Auth)
	}
}

func TestSecretsGoToKeychainNotFile(t *testing.T) {
	kc := NewMemoryKeychain()
	s := newTestStore(t, kc)
	if _, err := s.Upsert(Workspace{
		URL:  "https://acme.slack.com",
		Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-secret", XOXD: "xoxd-secret"},
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "xoxc-secret") || strings.Contains(string(raw), "xoxd-secret") {
		t.Errorf("plaintext secret leaked into file:\n%s", raw)
	}
	if !strings.Contains(string(raw), keychainPlaceholder) {
		t.Errorf("expected %s placeholder in file:\n%s", keychainPlaceholder, raw)
	}
	if v, ok := kc.Get(xoxcAccount("https://acme.slack.com")); !ok || v != "xoxc-secret" {
		t.Errorf("xoxc not stored in keychain: %q ok=%v", v, ok)
	}
	if v, ok := kc.Get(xoxdAccount); !ok || v != "xoxd-secret" {
		t.Errorf("xoxd not stored in keychain: %q ok=%v", v, ok)
	}

	// Reload hydrates from keychain.
	got, err := s.ResolveDefault()
	if err != nil {
		t.Fatal(err)
	}
	if got.Auth.XOXC != "xoxc-secret" || got.Auth.XOXD != "xoxd-secret" {
		t.Errorf("hydrated secrets wrong: %+v", got.Auth)
	}
}

func TestSecretsFallBackToFileWithoutKeychain(t *testing.T) {
	s := newTestStore(t, noopKeychain{})
	if _, err := s.Upsert(Workspace{
		URL:  "https://acme.slack.com",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-plain"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.Path())
	if !strings.Contains(string(raw), "xoxb-plain") {
		t.Errorf("without keychain the token should be in the file:\n%s", raw)
	}
}

func TestUpsertReplacesByNormalizedURL(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	_, _ = s.Upsert(Workspace{URL: "https://acme.slack.com", Auth: Auth{Type: AuthStandard, Token: "t1"}})
	_, _ = s.Upsert(Workspace{URL: "https://acme.slack.com/x", Name: "Acme", Auth: Auth{Type: AuthStandard, Token: "t2"}})

	creds, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(creds.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace after replace, got %d", len(creds.Workspaces))
	}
	if creds.Workspaces[0].Auth.Token != "t2" || creds.Workspaces[0].Name != "Acme" {
		t.Errorf("replace did not merge correctly: %+v", creds.Workspaces[0])
	}
}

func TestResolveSelector(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	_ = s.UpsertMany([]Workspace{
		{URL: "https://acme.slack.com", Name: "Acme Corp", Auth: Auth{Type: AuthStandard, Token: "a"}},
		{URL: "https://globex.slack.com", Name: "Globex", Auth: Auth{Type: AuthStandard, Token: "g"}},
	})

	cases := map[string]string{
		"https://globex.slack.com": "https://globex.slack.com", // exact URL
		"acme":                     "https://acme.slack.com",   // substring of host
		"globex":                   "https://globex.slack.com", // host without suffix
		"Acme Corp":                "https://acme.slack.com",   // name
	}
	for selector, want := range cases {
		got, err := s.Resolve(selector)
		if err != nil {
			t.Errorf("Resolve(%q) error: %v", selector, err)
			continue
		}
		if got.URL != want {
			t.Errorf("Resolve(%q) = %q, want %q", selector, got.URL, want)
		}
	}

	if _, err := s.Resolve("slack"); err == nil {
		t.Error("expected ambiguous error for selector matching both")
	} else if _, ok := err.(*AmbiguousSelectorError); !ok {
		t.Errorf("expected AmbiguousSelectorError, got %T: %v", err, err)
	}

	if _, err := s.Resolve("nonesuch"); err != ErrWorkspaceNotFound {
		t.Errorf("expected ErrWorkspaceNotFound, got %v", err)
	}
}

func TestRemove(t *testing.T) {
	kc := NewMemoryKeychain()
	s := newTestStore(t, kc)
	_ = s.UpsertMany([]Workspace{
		{URL: "https://acme.slack.com", Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-a", XOXD: "xoxd-d"}},
		{URL: "https://globex.slack.com", Auth: Auth{Type: AuthStandard, Token: "tok"}},
	})
	if err := s.Remove("https://acme.slack.com"); err != nil {
		t.Fatal(err)
	}
	creds, _ := s.Load()
	if len(creds.Workspaces) != 1 || creds.Workspaces[0].URL != "https://globex.slack.com" {
		t.Fatalf("remove failed: %+v", creds.Workspaces)
	}
	if creds.DefaultWorkspaceURL != "https://globex.slack.com" {
		t.Errorf("default not reassigned after removing default: %q", creds.DefaultWorkspaceURL)
	}
	if _, ok := kc.Get(xoxcAccount("https://acme.slack.com")); ok {
		t.Error("expected xoxc keychain entry deleted on remove")
	}
}

func TestRemoveKeepsSharedXOXDForOtherBrowserWorkspaces(t *testing.T) {
	kc := NewMemoryKeychain()
	s := newTestStore(t, kc)
	_ = s.UpsertMany([]Workspace{
		{URL: "https://acme.slack.com", Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-a", XOXD: "xoxd-shared"}},
		{URL: "https://globex.slack.com", Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-g", XOXD: "xoxd-shared"}},
	})
	if err := s.Remove("https://acme.slack.com"); err != nil {
		t.Fatal(err)
	}
	// The shared 'd' cookie is stored once under a single account; removing one
	// browser workspace must NOT delete it, or the survivor stops working.
	if _, ok := kc.Get(xoxdAccount); !ok {
		t.Error("shared xoxd must survive removal of one browser workspace")
	}
	if _, ok := kc.Get(xoxcAccount("https://acme.slack.com")); ok {
		t.Error("removed workspace's xoxc should be deleted")
	}
	creds, _ := s.Load()
	if len(creds.Workspaces) != 1 || creds.Workspaces[0].Auth.XOXD != "xoxd-shared" {
		t.Errorf("survivor lost its shared xoxd: %+v", creds.Workspaces)
	}
}

func TestSetIdentityPersistsIDsAndKeepsSecrets(t *testing.T) {
	kc := NewMemoryKeychain()
	s := newTestStore(t, kc)
	if _, err := s.Upsert(Workspace{
		URL:  "https://acme.slack.com",
		Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-secret", XOXD: "xoxd-secret"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetIdentity("https://acme.slack.com/", "T123", "U456"); err != nil {
		t.Fatal(err)
	}

	creds, _ := s.Load()
	w := creds.Workspaces[0]
	if w.TeamID != "T123" || w.UserID != "U456" {
		t.Fatalf("identity not persisted: team=%q user=%q", w.TeamID, w.UserID)
	}
	// Secrets must survive a SetIdentity that never carries Auth.
	if w.Auth.XOXC != "xoxc-secret" || w.Auth.XOXD != "xoxd-secret" {
		t.Fatalf("secrets clobbered by SetIdentity: %+v", w.Auth)
	}
}

func TestSetIdentityUnknownWorkspaceIsNoError(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	if err := s.SetIdentity("https://nope.slack.com", "T1", "U1"); err != nil {
		t.Fatalf("SetIdentity on unknown workspace should be a no-op, got %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	creds, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Version != 1 || len(creds.Workspaces) != 0 {
		t.Errorf("expected empty creds, got %+v", creds)
	}
}

func TestLoadCorruptFileIsEmpty(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err := s.Load()
	if err != nil {
		t.Fatalf("corrupt file should not error: %v", err)
	}
	if len(creds.Workspaces) != 0 {
		t.Errorf("corrupt file should yield empty creds, got %+v", creds)
	}
}

func TestSavedFileShape(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	_, _ = s.Upsert(Workspace{URL: "https://acme.slack.com", Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-a", XOXD: "xoxd-d"}})
	raw, _ := os.ReadFile(s.Path())
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
	if generic["version"].(float64) != 1 {
		t.Errorf("version = %v, want 1", generic["version"])
	}
	if _, ok := generic["updated_at"]; !ok {
		t.Error("expected updated_at timestamp")
	}
}

func TestSecretStatusesAndMissingSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	kc := NewMemoryKeychain()
	s := NewWithStore(path, kc)

	if err := s.UpsertMany([]Workspace{
		{URL: "https://acme.slack.com", Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-a", XOXD: "xoxd-a"}},
		{URL: "https://globex.slack.com", Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-g", XOXD: "xoxd-a"}},
	}); err != nil {
		t.Fatal(err)
	}
	// Orphan globex's placeholder — the legacy-migration failure shape.
	kc.Delete(xoxcAccount("https://globex.slack.com"))

	statuses, err := s.SecretStatuses()
	if err != nil {
		t.Fatal(err)
	}
	acme, globex := statuses["https://acme.slack.com"], statuses["https://globex.slack.com"]
	if acme["xoxc"] != SecretInKeychain || acme["xoxd"] != SecretInKeychain {
		t.Errorf("acme = %v", acme)
	}
	if globex["xoxc"] != SecretMissing || globex["xoxd"] != SecretInKeychain {
		t.Errorf("globex = %v", globex)
	}

	creds, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range creds.Workspaces {
		missing := MissingSecrets(w)
		if w.URL == "https://globex.slack.com" {
			if len(missing) != 1 || missing[0] != "xoxc" {
				t.Errorf("globex missing = %v", missing)
			}
		} else if len(missing) != 0 {
			t.Errorf("%s missing = %v", w.URL, missing)
		}
	}
}

func TestSecretStatusesFileFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := NewWithStore(path, noopKeychain{})
	if _, err := s.Upsert(Workspace{
		URL:  "https://acme.slack.com",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-plain"},
	}); err != nil {
		t.Fatal(err)
	}
	statuses, err := s.SecretStatuses()
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses["https://acme.slack.com"]["token"]; got != SecretInFile {
		t.Errorf("token status = %q, want file", got)
	}
}
