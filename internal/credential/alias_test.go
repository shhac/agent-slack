package credential

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- alias derivation and upsert keying ---

func TestUpsertDerivesAliasFromHost(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	saved, err := s.Upsert(Workspace{
		URL:  "https://acme.slack.com",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Alias != "acme" {
		t.Errorf("derived alias = %q, want acme", saved.Alias)
	}
}

func TestUpsertPrefersTeamDomainForAlias(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	saved, err := s.Upsert(Workspace{
		URL:        "https://acme.enterprise.slack.com",
		TeamDomain: "acme-eng",
		Auth:       Auth{Type: AuthStandard, Token: "xoxb-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Alias != "acme-eng" {
		t.Errorf("derived alias = %q, want acme-eng", saved.Alias)
	}
}

func TestTwoAliasesForOneURLCoexist(t *testing.T) {
	kc := NewMemoryKeychain()
	s := newTestStore(t, kc)
	for _, ws := range []Workspace{
		{Alias: "alice", URL: "https://acme.slack.com", Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-alice", XOXD: "xoxd-alice"}},
		{Alias: "bob", URL: "https://acme.slack.com", Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-bob", XOXD: "xoxd-bob"}},
	} {
		if _, err := s.Upsert(ws); err != nil {
			t.Fatal(err)
		}
	}

	creds, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(creds.Workspaces) != 2 {
		t.Fatalf("workspaces = %d, want 2 (same URL, distinct aliases)", len(creds.Workspaces))
	}
	byAlias := map[string]Workspace{}
	for _, w := range creds.Workspaces {
		byAlias[w.Alias] = w
	}
	if byAlias["alice"].Auth.XOXC != "xoxc-alice" || byAlias["bob"].Auth.XOXC != "xoxc-bob" {
		t.Errorf("per-alias secrets mixed up: %+v", byAlias)
	}
	// The d cookie is per-alias — no shared account.
	if v, ok := kc.entries["xoxd:alice"]; !ok || v != "xoxd-alice" {
		t.Errorf("xoxd:alice = %q, %v", v, ok)
	}
	if v, ok := kc.entries["xoxd:bob"]; !ok || v != "xoxd-bob" {
		t.Errorf("xoxd:bob = %q, %v", v, ok)
	}
	if _, ok := kc.entries["xoxd"]; ok {
		t.Error("legacy shared xoxd account written for a v2 store")
	}
}

func TestUpsertWithoutAliasUpdatesUniqueURLMatch(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	if _, err := s.Upsert(Workspace{Alias: "work", URL: "https://acme.slack.com",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-old"}}); err != nil {
		t.Fatal(err)
	}
	// An import (no alias) for the same URL updates the existing entry.
	saved, err := s.Upsert(Workspace{URL: "https://acme.slack.com",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-new"}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Alias != "work" {
		t.Errorf("alias = %q, want existing entry's alias work", saved.Alias)
	}
	creds, _ := s.Load()
	if len(creds.Workspaces) != 1 || creds.Workspaces[0].Auth.Token != "xoxb-new" {
		t.Errorf("expected single updated entry, got %+v", creds.Workspaces)
	}
}

func TestUpsertWithoutAliasAmbiguousURLErrors(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	for _, alias := range []string{"alice", "bob"} {
		if _, err := s.Upsert(Workspace{Alias: alias, URL: "https://acme.slack.com",
			Auth: Auth{Type: AuthStandard, Token: "xoxb-" + alias}}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Upsert(Workspace{URL: "https://acme.slack.com",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-new"}})
	var ambiguous *AmbiguousURLError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %v, want AmbiguousURLError", err)
	}
	if len(ambiguous.Aliases) != 2 {
		t.Errorf("aliases = %v", ambiguous.Aliases)
	}
}

func TestUpsertUniquifiesDerivedAliasCollision(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	// Same derived alias "acme" from two different URLs (so no URL match).
	if _, err := s.Upsert(Workspace{URL: "https://acme.slack.com",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-1"}}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Upsert(Workspace{URL: "https://acme.example.com", TeamDomain: "acme",
		Auth: Auth{Type: AuthStandard, Token: "xoxb-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Alias != "acme-2" {
		t.Errorf("alias = %q, want acme-2", saved.Alias)
	}
}

// --- resolution ---

func TestResolveExactAliasWinsOverFuzzy(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	for _, ws := range []Workspace{
		{Alias: "acme", URL: "https://acme.slack.com", Auth: Auth{Type: AuthStandard, Token: "xoxb-1"}},
		{Alias: "acme-bob", URL: "https://acme.slack.com", Auth: Auth{Type: AuthStandard, Token: "xoxb-2"}},
	} {
		if _, err := s.Upsert(ws); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Resolve("acme")
	if err != nil {
		t.Fatalf("exact alias should not be ambiguous: %v", err)
	}
	if got.Alias != "acme" {
		t.Errorf("resolved %q, want acme", got.Alias)
	}
}

func TestResolveAmbiguityListsAliases(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	for _, alias := range []string{"alice", "bob"} {
		if _, err := s.Upsert(Workspace{Alias: alias, URL: "https://acme.slack.com",
			Auth: Auth{Type: AuthStandard, Token: "xoxb-" + alias}}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Resolve("acme.slack.com")
	var ambiguous *AmbiguousSelectorError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("err = %v, want AmbiguousSelectorError", err)
	}
	joined := strings.Join(ambiguous.Matches, " ")
	if !strings.Contains(joined, "alice") || !strings.Contains(joined, "bob") {
		t.Errorf("matches should name aliases: %v", ambiguous.Matches)
	}
}

func TestSetDefaultStoresAlias(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	for _, alias := range []string{"one", "two"} {
		if _, err := s.Upsert(Workspace{Alias: alias, URL: "https://" + alias + ".slack.com",
			Auth: Auth{Type: AuthStandard, Token: "xoxb-" + alias}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetDefault("two"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Alias != "two" {
		t.Errorf("default = %q, want two", got.Alias)
	}
}

func TestRemoveByAliasKeepsOtherEntryForSameURL(t *testing.T) {
	kc := NewMemoryKeychain()
	s := newTestStore(t, kc)
	for _, alias := range []string{"alice", "bob"} {
		if _, err := s.Upsert(Workspace{Alias: alias, URL: "https://acme.slack.com",
			Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-" + alias, XOXD: "xoxd-" + alias}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Remove("alice"); err != nil {
		t.Fatal(err)
	}
	creds, _ := s.Load()
	if len(creds.Workspaces) != 1 || creds.Workspaces[0].Alias != "bob" {
		t.Fatalf("workspaces after remove = %+v", creds.Workspaces)
	}
	if _, ok := kc.entries["xoxd:alice"]; ok {
		t.Error("alice's xoxd not deleted")
	}
	if v, ok := kc.entries["xoxd:bob"]; !ok || v != "xoxd-bob" {
		t.Error("bob's xoxd should survive alice's removal")
	}
}

// --- v1 → v2 migration ---

func TestLoadMigratesV1File(t *testing.T) {
	kc := NewMemoryKeychain()
	kc.entries["xoxc:https://acme.slack.com"] = "xoxc-secret"
	kc.entries["xoxd"] = "xoxd-shared"
	kc.entries["token:https://beta.slack.com"] = "xoxb-secret"

	path := filepath.Join(t.TempDir(), "credentials.json")
	v1 := `{
  "version": 1,
  "default_workspace_url": "https://beta.slack.com",
  "workspaces": [
    {"workspace_url": "https://acme.slack.com", "workspace_name": "Acme", "team_domain": "acme",
     "auth": {"auth_type": "browser", "xoxc_token": "__KEYCHAIN__", "xoxd_cookie": "__KEYCHAIN__"}},
    {"workspace_url": "https://beta.slack.com", "workspace_name": "Beta",
     "auth": {"auth_type": "standard", "token": "__KEYCHAIN__"}}
  ]
}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewWithStore(path, kc)

	creds, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Version != 2 {
		t.Errorf("version = %d, want 2", creds.Version)
	}
	byAlias := map[string]Workspace{}
	for _, w := range creds.Workspaces {
		byAlias[w.Alias] = w
	}
	if byAlias["acme"].Auth.XOXC != "xoxc-secret" || byAlias["acme"].Auth.XOXD != "xoxd-shared" {
		t.Errorf("acme secrets not migrated: %+v", byAlias["acme"].Auth)
	}
	if byAlias["beta"].Auth.Token != "xoxb-secret" {
		t.Errorf("beta token not migrated: %+v", byAlias["beta"].Auth)
	}
	if creds.DefaultWorkspace != "beta" {
		t.Errorf("default = %q, want beta", creds.DefaultWorkspace)
	}

	// Keychain re-keyed per alias; legacy accounts gone (shared xoxd included).
	for _, want := range []string{"xoxc:acme", "xoxd:acme", "token:beta"} {
		if _, ok := kc.entries[want]; !ok {
			t.Errorf("missing migrated keychain account %s (have %v)", want, kc.entries)
		}
	}
	for _, gone := range []string{"xoxc:https://acme.slack.com", "token:https://beta.slack.com", "xoxd"} {
		if _, ok := kc.entries[gone]; ok {
			t.Errorf("legacy keychain account %s not removed", gone)
		}
	}

	// The file itself was rewritten as v2 (one-shot, not per-load).
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil || onDisk.Version != 2 {
		t.Errorf("on-disk version = %d (err %v), want rewritten 2", onDisk.Version, err)
	}
	if strings.Contains(string(raw), "xoxc-secret") {
		t.Error("migration leaked a secret into the file despite an available keychain")
	}
}

func TestSecretStatusesKeyedByAlias(t *testing.T) {
	s := newTestStore(t, NewMemoryKeychain())
	if _, err := s.Upsert(Workspace{Alias: "acme", URL: "https://acme.slack.com",
		Auth: Auth{Type: AuthBrowser, XOXC: "xoxc-1", XOXD: "xoxd-1"}}); err != nil {
		t.Fatal(err)
	}
	statuses, err := s.SecretStatuses()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := statuses["acme"]
	if !ok {
		t.Fatalf("statuses not keyed by alias: %v", statuses)
	}
	if st["xoxc"] != SecretInKeychain || st["xoxd"] != SecretInKeychain {
		t.Errorf("statuses = %v", st)
	}
}

func TestMigrateWithoutBrowserWorkspacesKeepsSharedXOXD(t *testing.T) {
	kc := NewMemoryKeychain()
	kc.entries["xoxd"] = "someone-elses-cookie" // owned by a different store on the same service
	kc.entries["token:https://beta.slack.com"] = "xoxb-secret"

	path := filepath.Join(t.TempDir(), "credentials.json")
	v1 := `{"version": 1, "workspaces": [
    {"workspace_url": "https://beta.slack.com",
     "auth": {"auth_type": "standard", "token": "__KEYCHAIN__"}}]}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithStore(path, kc).Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := kc.entries["xoxd"]; !ok {
		t.Error("migrating a token-only store must not delete the service-global xoxd account")
	}
}

// v1MigrationFixture writes a version-1 credentials file with one browser and
// one standard workspace, and seeds kc with their legacy accounts.
func v1MigrationFixture(t *testing.T, kc *MemoryKeychain) string {
	t.Helper()
	kc.Set("xoxc:https://acme.slack.com", "xoxc-secret")
	kc.Set("xoxd", "xoxd-shared")
	kc.Set("token:https://beta.slack.com", "xoxb-secret")
	path := filepath.Join(t.TempDir(), "credentials.json")
	v1 := `{
  "version": 1,
  "default_workspace_url": "https://beta.slack.com",
  "workspaces": [
    {"workspace_url": "https://acme.slack.com", "workspace_name": "Acme", "team_domain": "acme",
     "auth": {"auth_type": "browser", "xoxc_token": "__KEYCHAIN__", "xoxd_cookie": "__KEYCHAIN__"}},
    {"workspace_url": "https://beta.slack.com", "workspace_name": "Beta",
     "auth": {"auth_type": "standard", "token": "__KEYCHAIN__"}}
  ]
}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Every mutation must work against a version-1 file: migration happens on the
// way in, without deadlocking on the non-reentrant file lock. A regression
// here hangs rather than fails, so run each mutation under a timeout.
func TestMutationsOnV1FileMigrateWithoutDeadlock(t *testing.T) {
	mutations := map[string]func(s *Store) error{
		"upsert": func(s *Store) error {
			_, err := s.Upsert(Workspace{URL: "https://new.slack.com",
				Auth: Auth{Type: AuthStandard, Token: "xoxb-new"}})
			return err
		},
		"set-identity": func(s *Store) error { return s.SetIdentity("acme", "T1", "U1") },
		"set-default":  func(s *Store) error { return s.SetDefault("acme") },
		"remove":       func(s *Store) error { return s.Remove("acme") },
		"secret-statuses": func(s *Store) error {
			_, err := s.SecretStatuses()
			return err
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			kc := NewMemoryKeychain()
			s := NewWithStore(v1MigrationFixture(t, kc), kc)

			done := make(chan error, 1)
			go func() { done <- mutate(s) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s on v1 file: %v", name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s on v1 file deadlocked (lock reentrancy regression)", name)
			}

			creds, err := s.Load()
			if err != nil {
				t.Fatal(err)
			}
			if creds.Version != 2 {
				t.Errorf("version after %s = %d, want 2", name, creds.Version)
			}
		})
	}
}

// N stores racing on one v1 file must migrate exactly once: every workspace
// survives with its secrets re-homed per alias, and nothing is double-deleted.
func TestConcurrentLoadsMigrateV1Once(t *testing.T) {
	kc := NewMemoryKeychain()
	path := v1MigrationFixture(t, kc)

	const workers = 16
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = NewWithStore(path, kc).Load()
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}

	creds, err := NewWithStore(path, kc).Load()
	if err != nil {
		t.Fatal(err)
	}
	if creds.Version != 2 || len(creds.Workspaces) != 2 {
		t.Fatalf("after concurrent migration: version=%d workspaces=%d", creds.Version, len(creds.Workspaces))
	}
	byAlias := map[string]Workspace{}
	for _, w := range creds.Workspaces {
		byAlias[w.Alias] = w
	}
	if byAlias["acme"].Auth.XOXC != "xoxc-secret" || byAlias["acme"].Auth.XOXD != "xoxd-shared" {
		t.Errorf("acme secrets lost in concurrent migration: %+v", byAlias["acme"].Auth)
	}
	if byAlias["beta"].Auth.Token != "xoxb-secret" {
		t.Errorf("beta token lost in concurrent migration: %+v", byAlias["beta"].Auth)
	}
	entries := kc.snapshot()
	for _, want := range []string{"xoxc:acme", "xoxd:acme", "token:beta"} {
		if _, ok := entries[want]; !ok {
			t.Errorf("missing re-homed account %s after concurrent migration", want)
		}
	}
}

// Migration on a host whose keychain rejects writes must keep the legacy
// secrets it hydrated — in the file if nowhere else — never dropping them.
func TestMigrationWithFailingKeychainKeepsSecretsInFile(t *testing.T) {
	// failingKeychain rejects Sets but must still serve legacy Gets for
	// hydration, so build a read-only view over seeded data.
	kc := readOnlyKeychain{data: map[string]string{
		"xoxc:https://acme.slack.com": "xoxc-secret",
		"xoxd":                        "xoxd-shared",
	}}
	path := filepath.Join(t.TempDir(), "credentials.json")
	v1 := `{"version": 1, "workspaces": [
    {"workspace_url": "https://acme.slack.com", "team_domain": "acme",
     "auth": {"auth_type": "browser", "xoxc_token": "__KEYCHAIN__", "xoxd_cookie": "__KEYCHAIN__"}}]}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := NewWithStore(path, kc).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspaces[0].Auth.XOXC != "xoxc-secret" || got.Workspaces[0].Auth.XOXD != "xoxd-shared" {
		t.Fatalf("hydrated secrets = %+v", got.Workspaces[0].Auth)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "xoxc-secret") || !strings.Contains(string(raw), "xoxd-shared") {
		t.Errorf("secrets dropped during keychain-degraded migration:\n%s", raw)
	}
}

// Two v1 workspaces deriving the same alias must uniquify AND keep their
// secrets under their own final aliases — a mix-up here is a silent
// cross-workspace credential swap.
func TestMigrationUniquifiesCollidingAliasesWithSeparateSecrets(t *testing.T) {
	kc := NewMemoryKeychain()
	kc.Set("xoxc:https://acme.slack.com", "xoxc-one")
	kc.Set("xoxc:https://acme.example.com", "xoxc-two")
	kc.Set("xoxd", "xoxd-shared")

	path := filepath.Join(t.TempDir(), "credentials.json")
	v1 := `{"version": 1, "workspaces": [
    {"workspace_url": "https://acme.slack.com",
     "auth": {"auth_type": "browser", "xoxc_token": "__KEYCHAIN__", "xoxd_cookie": "__KEYCHAIN__"}},
    {"workspace_url": "https://acme.example.com", "team_domain": "acme",
     "auth": {"auth_type": "browser", "xoxc_token": "__KEYCHAIN__", "xoxd_cookie": "__KEYCHAIN__"}}]}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}

	creds, err := NewWithStore(path, kc).Load()
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{}
	for _, w := range creds.Workspaces {
		secrets[w.Alias] = w.Auth.XOXC
	}
	if secrets["acme"] != "xoxc-one" || secrets["acme-2"] != "xoxc-two" {
		t.Errorf("collision migration mixed up secrets: %v", secrets)
	}
	entries := kc.snapshot()
	if entries["xoxc:acme"] != "xoxc-one" || entries["xoxc:acme-2"] != "xoxc-two" {
		t.Errorf("re-homed accounts wrong: %v", entries)
	}
}

// relabelForV2 is the pure half of migration — table-test its edge cases
// directly, no Keychain or lock involved.
func TestRelabelForV2(t *testing.T) {
	cases := map[string]struct {
		file        credentialsFile
		wantAliases []string
		wantDefault string
	}{
		"collision uniquifies in order": {
			file: credentialsFile{Credentials: Credentials{Workspaces: []Workspace{
				{URL: "https://acme.slack.com"},
				{URL: "https://acme.example.com", TeamDomain: "acme"},
			}}},
			wantAliases: []string{"acme", "acme-2"},
		},
		"default url maps to alias": {
			file: credentialsFile{
				Credentials:      Credentials{Workspaces: []Workspace{{URL: "https://beta.slack.com"}}},
				LegacyDefaultURL: "https://beta.slack.com/some/path",
			},
			wantAliases: []string{"beta"},
			wantDefault: "beta",
		},
		"unresolvable default leaves it empty": {
			file: credentialsFile{
				Credentials:      Credentials{Workspaces: []Workspace{{URL: "https://beta.slack.com"}}},
				LegacyDefaultURL: "https://gone.slack.com",
			},
			wantAliases: []string{"beta"},
		},
		"unparseable url falls back to name": {
			file: credentialsFile{Credentials: Credentials{Workspaces: []Workspace{
				{URL: "not a url", Name: "My Team!"},
			}}},
			wantAliases: []string{"my-team"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			relabelForV2(&tc.file)
			for i, want := range tc.wantAliases {
				if got := tc.file.Workspaces[i].Alias; got != want {
					t.Errorf("alias[%d] = %q, want %q", i, got, want)
				}
			}
			if tc.file.DefaultWorkspace != tc.wantDefault {
				t.Errorf("default = %q, want %q", tc.file.DefaultWorkspace, tc.wantDefault)
			}
		})
	}
}
