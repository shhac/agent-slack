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
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	return &Store{path: path, kc: defaultKeychain(), now: time.Now}, nil
}

// NewWithStore builds a Store with an explicit file path and Keychain — used by
// tests to avoid touching the real config dir or Keychain.
func NewWithStore(path string, kc Keychain) *Store {
	return &Store{path: path, kc: kc, now: time.Now}
}

func defaultPath() (string, error) {
	if env := os.Getenv("AGENT_SLACK_CREDENTIALS"); env != "" {
		return env, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", herr
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "agent-slack", "credentials.json"), nil
}

// Path returns the credentials file path (for reporting, not secrets).
func (s *Store) Path() string { return s.path }

// normalizeURL reduces a workspace URL to scheme://host, dropping any path.
func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid workspace URL %q", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func isPlaceholder(v string) bool { return v == "" || v == keychainPlaceholder }

func xoxcAccount(normalizedURL string) string  { return "xoxc:" + normalizedURL }
func tokenAccount(normalizedURL string) string { return "token:" + normalizedURL }

const xoxdAccount = "xoxd"

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
		// xoxd is shared across browser workspaces; store it once.
		for _, w := range out.Workspaces {
			if w.Auth.Type == AuthBrowser && !isPlaceholder(w.Auth.XOXD) {
				s.kc.Set(xoxdAccount, w.Auth.XOXD)
				break
			}
		}
		for i := range out.Workspaces {
			w := &out.Workspaces[i]
			switch w.Auth.Type {
			case AuthBrowser:
				if !isPlaceholder(w.Auth.XOXC) && s.kc.Set(xoxcAccount(w.URL), w.Auth.XOXC) {
					w.Auth.XOXC = keychainPlaceholder
				}
				if !isPlaceholder(w.Auth.XOXD) {
					w.Auth.XOXD = keychainPlaceholder
				}
			case AuthStandard:
				if !isPlaceholder(w.Auth.Token) && s.kc.Set(tokenAccount(w.URL), w.Auth.Token) {
					w.Auth.Token = keychainPlaceholder
				}
			}
		}
	}

	data, err := json.MarshalIndent(&out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
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
	creds, err := s.Load()
	if err != nil {
		return Workspace{}, err
	}
	var last Workspace
	for _, ws := range workspaces {
		normalized, err := normalizeURL(ws.URL)
		if err != nil {
			return Workspace{}, err
		}
		ws.URL = normalized
		last = ws

		idx := -1
		for i, existing := range creds.Workspaces {
			if existing.URL == normalized {
				idx = i
				break
			}
		}
		if idx == -1 {
			creds.Workspaces = append(creds.Workspaces, ws)
		} else {
			merged := creds.Workspaces[idx]
			merged.URL = normalized
			if ws.Name != "" {
				merged.Name = ws.Name
			}
			if ws.TeamID != "" {
				merged.TeamID = ws.TeamID
			}
			if ws.TeamDomain != "" {
				merged.TeamDomain = ws.TeamDomain
			}
			merged.Auth = ws.Auth
			creds.Workspaces[idx] = merged
		}
		if creds.DefaultWorkspaceURL == "" {
			creds.DefaultWorkspaceURL = normalized
		}
	}
	return last, s.Save(creds)
}

// SetDefault sets the default workspace URL.
func (s *Store) SetDefault(workspaceURL string) error {
	creds, err := s.Load()
	if err != nil {
		return err
	}
	normalized, err := normalizeURL(workspaceURL)
	if err != nil {
		return err
	}
	creds.DefaultWorkspaceURL = normalized
	return s.Save(creds)
}

// Remove deletes a workspace and its Keychain secrets.
func (s *Store) Remove(workspaceURL string) error {
	creds, err := s.Load()
	if err != nil {
		return err
	}
	normalized, err := normalizeURL(workspaceURL)
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
}

// ResolveDefault returns the default workspace, or the first one.
func (s *Store) ResolveDefault() (*Workspace, error) {
	creds, err := s.Load()
	if err != nil {
		return nil, err
	}
	if creds.DefaultWorkspaceURL != "" {
		for i := range creds.Workspaces {
			if creds.Workspaces[i].URL == creds.DefaultWorkspaceURL {
				return &creds.Workspaces[i], nil
			}
		}
	}
	if len(creds.Workspaces) > 0 {
		return &creds.Workspaces[0], nil
	}
	return nil, ErrWorkspaceNotFound
}

// Resolve picks a workspace for a --workspace selector. An empty selector
// returns the default. A selector is matched as: exact normalized-URL match,
// else a unique case-insensitive substring of the URL, host, host without the
// .slack.com suffix, name, or team domain.
func (s *Store) Resolve(selector string) (*Workspace, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return s.ResolveDefault()
	}
	creds, err := s.Load()
	if err != nil {
		return nil, err
	}

	if normalized, nerr := normalizeURL(selector); nerr == nil {
		want := strings.ToLower(normalized)
		for i := range creds.Workspaces {
			if strings.ToLower(creds.Workspaces[i].URL) == want {
				return &creds.Workspaces[i], nil
			}
		}
	}

	needle := strings.ToLower(selector)
	var matches []int
	for i := range creds.Workspaces {
		for _, cand := range selectorCandidates(creds.Workspaces[i]) {
			if cand != "" && strings.Contains(cand, needle) {
				matches = append(matches, i)
				break
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, ErrWorkspaceNotFound
	case 1:
		return &creds.Workspaces[matches[0]], nil
	default:
		labels := make([]string, len(matches))
		for j, idx := range matches {
			labels[j] = creds.Workspaces[idx].URL
		}
		sort.Strings(labels)
		return nil, &AmbiguousSelectorError{Selector: selector, Matches: labels}
	}
}

func selectorCandidates(w Workspace) []string {
	host := ""
	if u, err := url.Parse(w.URL); err == nil {
		host = strings.ToLower(u.Host)
	}
	return []string{
		strings.ToLower(w.URL),
		host,
		strings.TrimSuffix(host, ".slack.com"),
		strings.ToLower(w.Name),
		strings.ToLower(w.TeamDomain),
	}
}
