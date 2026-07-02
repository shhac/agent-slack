// Package credential manages agent-slack's stored Slack credentials.
//
// Non-secret workspace metadata lives in a JSON file under the user config dir
// (~/.config/agent-slack/credentials.json by default). Secrets (tokens and the
// xoxd cookie) are stored in the macOS Keychain when available; the file then
// holds a "__KEYCHAIN__" placeholder in their place. On platforms without a
// supported Keychain the secrets are written to the file directly.
package credential

import (
	"errors"
	"os"
	"time"

	"github.com/shhac/agent-slack/internal/fslock"
)

// Store reads and writes the credentials file plus the backing Keychain.
type Store struct {
	path string
	kc   Keychain
	now  func() time.Time
}

// New returns a Store using the default credentials path and platform Keychain.
func New() (*Store, error) {
	path, err := defaultPath()
	if err != nil {
		return nil, err
	}
	if os.Getenv("AGENT_SLACK_CREDENTIALS") == "" {
		migrateLegacyFile(path)
	}
	return &Store{path: path, kc: defaultKeychain(), now: time.Now}, nil
}

// NewWithStore builds a Store with an explicit file path and Keychain — used by
// tests to avoid touching the real config dir or Keychain.
func NewWithStore(path string, kc Keychain) *Store {
	return &Store{path: path, kc: kc, now: time.Now}
}

// Load reads the credentials file and hydrates secrets from the Keychain. A
// version-1 file is migrated (one-shot, under the file lock) first.
func (s *Store) Load() (*Credentials, error) {
	if err := s.migrateIfNeeded(); err != nil {
		return nil, err
	}
	return s.load()
}

// load reads and hydrates without locking or migrating — the lock-free read
// path, also safe to call while the write lock is held.
func (s *Store) load() (*Credentials, error) {
	file, err := s.readFile()
	if err != nil {
		return nil, err
	}
	creds := file.Credentials
	for i := range creds.Workspaces {
		for _, ref := range creds.Workspaces[i].secretRefs() {
			if v, ok := s.kc.Get(ref.account); ok {
				*ref.value = v
			}
		}
	}
	return &creds, nil
}

// secretRef pairs one secret field of a workspace with the Keychain account
// backing it; value points into the Workspace so hydrate/push mutate in place.
type secretRef struct {
	name    string // "token", "xoxc", "xoxd" — the auth-list/status key
	account string
	value   *string
}

// secretRefs enumerates the secrets w's auth type uses — the single source of
// truth for the auth-type → field → Keychain-account mapping. Callers needing
// the in-place mutation must invoke it on the stored element, not a copy.
func (w *Workspace) secretRefs() []secretRef {
	if w.Auth.Type == AuthBrowser {
		return []secretRef{
			{name: "xoxc", account: xoxcAccount(w.Alias), value: &w.Auth.XOXC},
			{name: "xoxd", account: xoxdAccount(w.Alias), value: &w.Auth.XOXD},
		}
	}
	return []secretRef{{name: "token", account: tokenAccount(w.Alias), value: &w.Auth.Token}}
}

// migrateIfNeeded checks the version lock-free and takes the lock only when a
// legacy file needs upgrading; migrateLocked re-checks under the lock, so
// concurrent processes migrate exactly once.
func (s *Store) migrateIfNeeded() error {
	file, err := s.readFile()
	if err != nil {
		return err
	}
	if file.Version >= storeVersion {
		return nil
	}
	return s.withLock(s.migrateLocked)
}

// readFile parses the raw credentials file without touching the Keychain. A
// missing or corrupt file reads as an empty current-version store, matching
// the original's permissive behavior (and needing no migration).
func (s *Store) readFile() (*credentialsFile, error) {
	file, ok, err := fslock.ReadJSON[credentialsFile](s.path)
	if err != nil {
		return nil, err
	}
	if !ok {
		file.Version = storeVersion
		file.Workspaces = []Workspace{}
		return file, nil
	}
	if file.Version == 0 {
		file.Version = 1
	}
	return file, nil
}

// Save writes the credentials, pushing secrets to the Keychain where possible
// and replacing them with a placeholder in the file.
func (s *Store) Save(creds *Credentials) error {
	out := *creds
	out.Version = storeVersion
	out.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	out.Workspaces = make([]Workspace, len(creds.Workspaces))
	copy(out.Workspaces, creds.Workspaces)

	for i := range out.Workspaces {
		if n, err := normalizeURL(out.Workspaces[i].URL); err == nil {
			out.Workspaces[i].URL = n
		}
	}

	if s.kc.Available() {
		s.pushSecretsToKeychain(out.Workspaces)
	}

	// Atomic replace: Load is lock-free, and a torn read there degrades to
	// "empty store" — which a later Save would happily persist.
	return fslock.WriteJSON(s.path, &out)
}

// withLock serializes this store's read-modify-write cycles against other
// processes (e.g. parallel MCP tool-call subprocesses) mutating the same
// file. The lock is not reentrant, so locked sections use the lock-free
// load/Save/migrateLocked primitives, never Load or edit.
func (s *Store) withLock(fn func() error) error {
	return fslock.WithLock(s.path, fn)
}

// errNoChange lets an edit callback signal "nothing to persist": edit treats
// it as success and skips the Save (no updated_at churn for no-op writes).
var errNoChange = errors.New("no change")

// edit is the single write primitive: one hold of the cross-process lock
// covers migrate-if-legacy, load, the caller's mutation, and the save — so a
// concurrent writer can't interleave anywhere in the cycle.
func (s *Store) edit(fn func(*Credentials) error) error {
	return s.withLock(func() error {
		if err := s.migrateLocked(); err != nil {
			return err
		}
		creds, err := s.load()
		if err != nil {
			return err
		}
		if err := fn(creds); err != nil {
			if errors.Is(err, errNoChange) {
				return nil
			}
			return err
		}
		return s.Save(creds)
	})
}

// pushSecretsToKeychain stores each workspace's secrets in the Keychain and
// replaces the in-place file copy with the placeholder — but only for secrets
// the Keychain actually accepted; a failed Set leaves the real value in the
// file so it is never lost. Every account (the d cookie included) is keyed by
// alias. The caller is responsible for checking s.kc.Available() first.
func (s *Store) pushSecretsToKeychain(workspaces []Workspace) {
	for i := range workspaces {
		for _, ref := range workspaces[i].secretRefs() {
			if !isPlaceholder(*ref.value) && s.kc.Set(ref.account, *ref.value) {
				*ref.value = keychainPlaceholder
			}
		}
	}
}
