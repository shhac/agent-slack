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

type Workspace struct {
	URL        string `json:"workspace_url"`
	Name       string `json:"workspace_name,omitempty"`
	TeamID     string `json:"team_id,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	TeamDomain string `json:"team_domain,omitempty"`
	Auth       Auth   `json:"auth"`
}

type Credentials struct {
	Version             int         `json:"version"`
	UpdatedAt           string      `json:"updated_at,omitempty"`
	DefaultWorkspaceURL string      `json:"default_workspace_url,omitempty"`
	Workspaces          []Workspace `json:"workspaces"`
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

// Load reads the credentials file and hydrates secrets from the Keychain.
func (s *Store) Load() (*Credentials, error) {
	creds := &Credentials{Version: 1, Workspaces: []Workspace{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return creds, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, creds); err != nil {
		// A corrupt file is treated as empty rather than fatal, matching the
		// permissive behavior of the original.
		return &Credentials{Version: 1, Workspaces: []Workspace{}}, nil
	}
	if creds.Version == 0 {
		creds.Version = 1
	}

	for i := range creds.Workspaces {
		w := &creds.Workspaces[i]
		switch w.Auth.Type {
		case AuthBrowser:
			if v, ok := s.kc.Get(xoxcAccount(w.URL)); ok {
				w.Auth.XOXC = v
			}
			if v, ok := s.kc.Get(xoxdAccount); ok {
				w.Auth.XOXD = v
			}
		case AuthStandard:
			if v, ok := s.kc.Get(tokenAccount(w.URL)); ok {
				w.Auth.Token = v
			}
		}
	}
	return creds, nil
}

// Save writes the credentials, pushing secrets to the Keychain where possible
// and replacing them with a placeholder in the file.
func (s *Store) Save(creds *Credentials) error {
	out := *creds
	out.Version = 1
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
func (s *Store) withLock(fn func() error) error {
	return fslock.WithLock(s.path, fn)
}

// pushSecretsToKeychain stores each workspace's secrets in the Keychain and
// replaces the in-place file copy with the placeholder — but only for secrets
// the Keychain actually accepted; a failed Set leaves the real value in the
// file so it is never lost. xoxd is shared across browser workspaces and stored
// once. The caller is responsible for checking s.kc.Available() first.
func (s *Store) pushSecretsToKeychain(workspaces []Workspace) {
	xoxdStored := false
	for _, w := range workspaces {
		if w.Auth.Type == AuthBrowser && !isPlaceholder(w.Auth.XOXD) {
			xoxdStored = s.kc.Set(xoxdAccount, w.Auth.XOXD)
			break
		}
	}
	for i := range workspaces {
		w := &workspaces[i]
		switch w.Auth.Type {
		case AuthBrowser:
			if !isPlaceholder(w.Auth.XOXC) && s.kc.Set(xoxcAccount(w.URL), w.Auth.XOXC) {
				w.Auth.XOXC = keychainPlaceholder
			}
			if !isPlaceholder(w.Auth.XOXD) && xoxdStored {
				w.Auth.XOXD = keychainPlaceholder
			}
		case AuthStandard:
			if !isPlaceholder(w.Auth.Token) && s.kc.Set(tokenAccount(w.URL), w.Auth.Token) {
				w.Auth.Token = keychainPlaceholder
			}
		}
	}
}

// Upsert inserts or replaces a workspace by normalized URL and persists.
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
			last = ws

			if idx := findWorkspaceIndex(creds.Workspaces, normalized); idx == -1 {
				creds.Workspaces = append(creds.Workspaces, ws)
			} else {
				creds.Workspaces[idx] = mergeWorkspace(creds.Workspaces[idx], ws)
			}
			if creds.DefaultWorkspaceURL == "" {
				creds.DefaultWorkspaceURL = normalized
			}
		}
		return s.Save(creds)
	})
	if err != nil {
		return Workspace{}, err
	}
	return last, nil
}

// findWorkspaceIndex returns the index of the workspace with the given
// normalized URL, or -1 when none matches.
func findWorkspaceIndex(workspaces []Workspace, normalizedURL string) int {
	for i := range workspaces {
		if workspaces[i].URL == normalizedURL {
			return i
		}
	}
	return -1
}

// mergeWorkspace overlays incoming onto existing for an upsert: non-empty
// metadata fields win, and Auth is replaced wholesale (an upsert always carries
// the fresh secrets). incoming.URL is already normalized by the caller.
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

// SetIdentity records the Slack team_id/user_id (resolved from auth.test) on the
// matching workspace and persists. These are non-secret and key the on-disk
// cache namespace. It deliberately never touches Auth, so a best-effort identity
// backfill can't clobber stored secrets. An unknown workspace is a no-op.
func (s *Store) SetIdentity(workspaceURL, teamID, userID string) error {
	normalized, err := normalizeURL(workspaceURL)
	if err != nil {
		return err
	}
	return s.withLock(func() error {
		creds, err := s.Load()
		if err != nil {
			return err
		}
		idx := findWorkspaceIndex(creds.Workspaces, normalized)
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

// SetDefault sets the default workspace URL.
func (s *Store) SetDefault(workspaceURL string) error {
	normalized, err := normalizeURL(workspaceURL)
	if err != nil {
		return err
	}
	return s.withLock(func() error {
		creds, err := s.Load()
		if err != nil {
			return err
		}
		creds.DefaultWorkspaceURL = normalized
		return s.Save(creds)
	})
}

// Remove deletes a workspace and its Keychain secrets.
func (s *Store) Remove(workspaceURL string) error {
	normalized, err := normalizeURL(workspaceURL)
	if err != nil {
		return err
	}
	return s.withLock(func() error {
		creds, err := s.Load()
		if err != nil {
			return err
		}
		kept := creds.Workspaces[:0]
		for _, w := range creds.Workspaces {
			if w.URL == normalized {
				s.kc.Delete(xoxcAccount(w.URL))
				s.kc.Delete(tokenAccount(w.URL))
				continue
			}
			kept = append(kept, w)
		}
		creds.Workspaces = kept
		if creds.DefaultWorkspaceURL == normalized {
			creds.DefaultWorkspaceURL = ""
			if len(creds.Workspaces) > 0 {
				creds.DefaultWorkspaceURL = creds.Workspaces[0].URL
			}
		}
		return s.Save(creds)
	})
}
