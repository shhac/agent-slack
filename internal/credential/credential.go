// Package credential manages agent-slack's stored Slack credentials.
//
// Non-secret workspace metadata lives in a JSON file under the user config dir
// (~/.config/agent-slack/credentials.json by default). Secrets (tokens and the
// xoxd cookie) are stored in the macOS Keychain when available; the file then
// holds a "__KEYCHAIN__" placeholder in their place. On platforms without a
// supported Keychain the secrets are written to the file directly.
package credential

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shhac/agent-slack/internal/fslock"
)

type AuthType string

const (
	AuthBrowser  AuthType = "browser"
	AuthStandard AuthType = "standard"
)

// Auth holds the secrets for one workspace. Exactly one shape is valid per
// Type: browser uses XOXC+XOXD, standard uses Token.
type Auth struct {
	Type  AuthType `json:"auth_type"`
	Token string   `json:"token,omitempty"`
	XOXC  string   `json:"xoxc_token,omitempty"`
	XOXD  string   `json:"xoxd_cookie,omitempty"`
}

// Workspace is one named credential set. Alias is the unique key; several
// aliases may share a URL (two humans in the same Slack workspace).
type Workspace struct {
	Alias      string `json:"alias"`
	URL        string `json:"workspace_url"`
	Name       string `json:"workspace_name,omitempty"`
	TeamID     string `json:"team_id,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	TeamDomain string `json:"team_domain,omitempty"`
	Auth       Auth   `json:"auth"`
}

type Credentials struct {
	Version          int         `json:"version"`
	UpdatedAt        string      `json:"updated_at,omitempty"`
	DefaultWorkspace string      `json:"default_workspace,omitempty"`
	Workspaces       []Workspace `json:"workspaces"`
}

// storeVersion is the current credentials-file format: alias-keyed
// workspaces with per-alias Keychain accounts. See
// design-docs/workspace-aliases.md.
const storeVersion = 2

// credentialsFile is the on-disk shape across versions: current fields plus
// the version-1 default field, which migration maps onto an alias.
type credentialsFile struct {
	Credentials
	LegacyDefaultURL string `json:"default_workspace_url,omitempty"`
}

// ErrWorkspaceNotFound is returned when no stored workspace matches a request.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// AmbiguousSelectorError is returned when a --workspace selector matches more
// than one stored workspace.
type AmbiguousSelectorError struct {
	Selector string
	Matches  []string
}

func (e *AmbiguousSelectorError) Error() string {
	return fmt.Sprintf("workspace selector %q is ambiguous; matches: %s", e.Selector, strings.Join(e.Matches, ", "))
}

// AmbiguousURLError is returned when an alias-less upsert (an import) hits a
// URL that several stored aliases share — guessing which entry to overwrite
// would be a cross-user credential write.
type AmbiguousURLError struct {
	URL     string
	Aliases []string
}

func (e *AmbiguousURLError) Error() string {
	return fmt.Sprintf("several stored workspaces use %s (%s); pass an explicit alias", e.URL, strings.Join(e.Aliases, ", "))
}

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
	file, exists, err := s.readFile()
	if err != nil {
		return nil, err
	}
	if exists && file.Version < storeVersion {
		if err := s.migrateV1(); err != nil {
			return nil, err
		}
		if file, _, err = s.readFile(); err != nil {
			return nil, err
		}
	}

	creds := file.Credentials
	for i := range creds.Workspaces {
		w := &creds.Workspaces[i]
		switch w.Auth.Type {
		case AuthBrowser:
			if v, ok := s.kc.Get(xoxcAccount(w.Alias)); ok {
				w.Auth.XOXC = v
			}
			if v, ok := s.kc.Get(xoxdAccount(w.Alias)); ok {
				w.Auth.XOXD = v
			}
		case AuthStandard:
			if v, ok := s.kc.Get(tokenAccount(w.Alias)); ok {
				w.Auth.Token = v
			}
		}
	}
	return &creds, nil
}

// readFile parses the raw credentials file without touching the Keychain.
// exists reports whether a parseable file was found; a missing or corrupt
// file reads as an empty current-version store, matching the original's
// permissive behavior.
func (s *Store) readFile() (*credentialsFile, bool, error) {
	empty := &credentialsFile{Credentials: Credentials{Version: storeVersion, Workspaces: []Workspace{}}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, false, nil
		}
		return nil, false, err
	}
	file := &credentialsFile{}
	if err := json.Unmarshal(data, file); err != nil {
		return empty, false, nil
	}
	if file.Version == 0 {
		file.Version = 1
	}
	return file, true, nil
}

// migrateV1 rewrites a version-1 store as version 2: every workspace gets a
// derived alias, the URL-keyed (and shared-xoxd) Keychain accounts move to
// per-alias accounts, and default_workspace_url maps to an alias. Runs under
// the cross-process lock and re-checks the version inside it, so concurrent
// processes migrate exactly once. A legacy secret the Keychain won't return
// stays a placeholder — the workspace then reports "missing", same as any
// dangling placeholder, and heals via the usual re-import paths.
func (s *Store) migrateV1() error {
	return s.withLock(func() error {
		file, exists, err := s.readFile()
		if err != nil {
			return err
		}
		if !exists || file.Version >= storeVersion {
			return nil
		}

		taken := map[string]bool{}
		for i := range file.Workspaces {
			w := &file.Workspaces[i]
			if n, nerr := normalizeURL(w.URL); nerr == nil {
				w.URL = n
			}
			w.Alias = uniquifyAlias(deriveAlias(*w), func(a string) bool { return taken[a] })
			taken[w.Alias] = true

			// Hydrate from the legacy accounts so Save re-homes each secret
			// under the alias account (or keeps it in the file if the
			// Keychain rejects the write — never lost either way).
			switch w.Auth.Type {
			case AuthBrowser:
				if isPlaceholder(w.Auth.XOXC) {
					if v, ok := s.kc.Get(legacyXoxcAccount(w.URL)); ok {
						w.Auth.XOXC = v
					}
				}
				if isPlaceholder(w.Auth.XOXD) {
					if v, ok := s.kc.Get(legacyXoxdAccount); ok {
						w.Auth.XOXD = v
					}
				}
			case AuthStandard:
				if isPlaceholder(w.Auth.Token) {
					if v, ok := s.kc.Get(legacyTokenAccount(w.URL)); ok {
						w.Auth.Token = v
					}
				}
			}
		}

		if file.LegacyDefaultURL != "" {
			if n, nerr := normalizeURL(file.LegacyDefaultURL); nerr == nil {
				for _, w := range file.Workspaces {
					if w.URL == n {
						file.DefaultWorkspace = w.Alias
						break
					}
				}
			}
		}

		hadBrowser := false
		for _, w := range file.Workspaces {
			s.kc.Delete(legacyXoxcAccount(w.URL))
			s.kc.Delete(legacyTokenAccount(w.URL))
			hadBrowser = hadBrowser || w.Auth.Type == AuthBrowser
		}
		// The shared cookie account is service-global, not per-file: only the
		// migration that actually re-homed browser workspaces may delete it —
		// migrating an unrelated store (e.g. via AGENT_SLACK_CREDENTIALS) must
		// not orphan the main store's cookie.
		if hadBrowser {
			s.kc.Delete(legacyXoxdAccount)
		}

		return s.Save(&file.Credentials)
	})
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

	data, err := json.MarshalIndent(&out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	// Atomic replace: Load is lock-free, and a torn read there degrades to
	// "empty store" — which a later Save would happily persist.
	return fslock.WriteFile(s.path, data, 0o600)
}

// withLock serializes this store's read-modify-write cycles against other
// processes (e.g. parallel MCP tool-call subprocesses) mutating the same file.
// The lock is not reentrant: a locked section must not call Load on a
// version-1 file (which would try to take the lock again to migrate) —
// mutations call ensureMigrated first, so Load inside their locked section is
// migration-free.
func (s *Store) withLock(fn func() error) error {
	return fslock.WithLock(s.path, fn)
}

// ensureMigrated upgrades a version-1 file before a mutation takes the lock.
func (s *Store) ensureMigrated() error {
	file, exists, err := s.readFile()
	if err != nil {
		return err
	}
	if exists && file.Version < storeVersion {
		return s.migrateV1()
	}
	return nil
}

// pushSecretsToKeychain stores each workspace's secrets in the Keychain and
// replaces the in-place file copy with the placeholder — but only for secrets
// the Keychain actually accepted; a failed Set leaves the real value in the
// file so it is never lost. Every account (the d cookie included) is keyed by
// alias. The caller is responsible for checking s.kc.Available() first.
func (s *Store) pushSecretsToKeychain(workspaces []Workspace) {
	for i := range workspaces {
		w := &workspaces[i]
		switch w.Auth.Type {
		case AuthBrowser:
			if !isPlaceholder(w.Auth.XOXC) && s.kc.Set(xoxcAccount(w.Alias), w.Auth.XOXC) {
				w.Auth.XOXC = keychainPlaceholder
			}
			if !isPlaceholder(w.Auth.XOXD) && s.kc.Set(xoxdAccount(w.Alias), w.Auth.XOXD) {
				w.Auth.XOXD = keychainPlaceholder
			}
		case AuthStandard:
			if !isPlaceholder(w.Auth.Token) && s.kc.Set(tokenAccount(w.Alias), w.Auth.Token) {
				w.Auth.Token = keychainPlaceholder
			}
		}
	}
}

// Upsert inserts or replaces a workspace by alias and persists. An alias-less
// workspace (an import) updates the entry that uniquely holds its URL, gets a
// derived alias when the URL is new, and fails with AmbiguousURLError when
// several aliases share the URL.
func (s *Store) Upsert(ws Workspace) (Workspace, error) {
	return s.upsertMany([]Workspace{ws})
}

// UpsertMany inserts or replaces several workspaces in a single save.
func (s *Store) UpsertMany(workspaces []Workspace) error {
	if len(workspaces) == 0 {
		return nil
	}
	_, err := s.upsertMany(workspaces)
	return err
}

func (s *Store) upsertMany(workspaces []Workspace) (Workspace, error) {
	if err := s.ensureMigrated(); err != nil {
		return Workspace{}, err
	}
	var last Workspace
	err := s.withLock(func() error {
		creds, err := s.Load()
		if err != nil {
			return err
		}
		for _, ws := range workspaces {
			normalized, err := normalizeURL(ws.URL)
			if err != nil {
				return err
			}
			ws.URL = normalized

			idx, err := upsertTarget(creds.Workspaces, &ws)
			if err != nil {
				return err
			}
			if idx == -1 {
				creds.Workspaces = append(creds.Workspaces, ws)
			} else {
				creds.Workspaces[idx] = mergeWorkspace(creds.Workspaces[idx], ws)
			}
			last = ws
			if creds.DefaultWorkspace == "" {
				creds.DefaultWorkspace = ws.Alias
			}
		}
		return s.Save(creds)
	})
	if err != nil {
		return Workspace{}, err
	}
	return last, nil
}

// upsertTarget decides which stored entry an upsert lands on, filling in
// ws.Alias along the way. Returns -1 when ws is a new entry. An explicit
// alias keys directly; an alias-less workspace adopts the alias of the single
// entry holding its (already normalized) URL, derives a fresh alias when the
// URL is unknown, and refuses when several aliases share the URL.
func upsertTarget(stored []Workspace, ws *Workspace) (int, error) {
	if alias := slugify(ws.Alias); alias != "" {
		ws.Alias = alias
		return findAliasIndex(stored, alias), nil
	}

	var urlMatches []int
	for i := range stored {
		if stored[i].URL == ws.URL {
			urlMatches = append(urlMatches, i)
		}
	}
	switch len(urlMatches) {
	case 1:
		ws.Alias = stored[urlMatches[0]].Alias
		return urlMatches[0], nil
	case 0:
		ws.Alias = uniquifyAlias(deriveAlias(*ws), func(a string) bool {
			return findAliasIndex(stored, a) != -1
		})
		return -1, nil
	default:
		aliases := make([]string, len(urlMatches))
		for j, idx := range urlMatches {
			aliases[j] = stored[idx].Alias
		}
		return 0, &AmbiguousURLError{URL: ws.URL, Aliases: aliases}
	}
}

// findAliasIndex returns the index of the workspace with the given alias, or
// -1 when none matches.
func findAliasIndex(workspaces []Workspace, alias string) int {
	for i := range workspaces {
		if workspaces[i].Alias == alias {
			return i
		}
	}
	return -1
}

// mergeWorkspace overlays incoming onto existing for an upsert: non-empty
// metadata fields win, and Auth is replaced wholesale (an upsert always carries
// the fresh secrets). incoming.URL is already normalized and incoming.Alias
// resolved by the caller.
func mergeWorkspace(existing, incoming Workspace) Workspace {
	existing.URL = incoming.URL
	if incoming.Name != "" {
		existing.Name = incoming.Name
	}
	if incoming.TeamID != "" {
		existing.TeamID = incoming.TeamID
	}
	if incoming.UserID != "" {
		existing.UserID = incoming.UserID
	}
	if incoming.TeamDomain != "" {
		existing.TeamDomain = incoming.TeamDomain
	}
	existing.Auth = incoming.Auth
	return existing
}

// SetIdentity records the Slack team_id/user_id (resolved from auth.test) on
// the aliased workspace and persists. These are non-secret and key the
// on-disk cache namespace. It deliberately never touches Auth, so a
// best-effort identity backfill can't clobber stored secrets. An unknown
// alias is a no-op.
func (s *Store) SetIdentity(alias, teamID, userID string) error {
	if err := s.ensureMigrated(); err != nil {
		return err
	}
	return s.withLock(func() error {
		creds, err := s.Load()
		if err != nil {
			return err
		}
		idx := findAliasIndex(creds.Workspaces, alias)
		if idx == -1 {
			return nil
		}
		w := &creds.Workspaces[idx]
		changed := false
		if teamID != "" && w.TeamID != teamID {
			w.TeamID = teamID
			changed = true
		}
		if userID != "" && w.UserID != userID {
			w.UserID = userID
			changed = true
		}
		if !changed {
			return nil
		}
		return s.Save(creds)
	})
}

// SetDefault resolves selector to a stored workspace and makes its alias the
// default.
func (s *Store) SetDefault(selector string) error {
	if err := s.ensureMigrated(); err != nil {
		return err
	}
	return s.withLock(func() error {
		creds, err := s.Load()
		if err != nil {
			return err
		}
		ws, err := resolveWorkspace(creds, selector)
		if err != nil {
			return err
		}
		creds.DefaultWorkspace = ws.Alias
		return s.Save(creds)
	})
}

// Remove resolves selector to one stored workspace and deletes it along with
// its Keychain secrets. Other aliases for the same URL are untouched.
func (s *Store) Remove(selector string) error {
	if err := s.ensureMigrated(); err != nil {
		return err
	}
	return s.withLock(func() error {
		creds, err := s.Load()
		if err != nil {
			return err
		}
		ws, err := resolveWorkspace(creds, selector)
		if err != nil {
			return err
		}
		alias := ws.Alias
		kept := creds.Workspaces[:0]
		for _, w := range creds.Workspaces {
			if w.Alias == alias {
				s.kc.Delete(xoxcAccount(alias))
				s.kc.Delete(tokenAccount(alias))
				s.kc.Delete(xoxdAccount(alias))
				continue
			}
			kept = append(kept, w)
		}
		creds.Workspaces = kept
		if creds.DefaultWorkspace == alias {
			creds.DefaultWorkspace = ""
			if len(creds.Workspaces) > 0 {
				creds.DefaultWorkspace = creds.Workspaces[0].Alias
			}
		}
		return s.Save(creds)
	})
}
