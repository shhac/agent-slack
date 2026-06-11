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

func TestRedact(t *testing.T) {
	if got := Redact("xoxc-1234567890abcdef"); got != "xoxc-1…cdef" {
		t.Errorf("Redact long = %q", got)
	}
	if got := Redact("short"); got != "[redacted]" {
		t.Errorf("Redact short = %q, want [redacted]", got)
	}
	if got := Redact(""); got != "" {
		t.Errorf("Redact empty = %q", got)
	}
}
