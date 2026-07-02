// One-shot migration of a version-1 (URL-keyed, shared-xoxd) store to the
// alias-keyed version-2 format, plus the seeding of a missing store from the
// TypeScript tool's legacy file. See design-docs/workspace-aliases.md.
package credential

import (
	"os"
	"path/filepath"

	"github.com/shhac/lib-agent-cli/xdg"
)

// migrateLocked rewrites a version-1 store as version 2, assuming the caller
// holds the write lock: every workspace gets a derived alias, the URL-keyed
// (and shared-xoxd) Keychain accounts move to per-alias accounts, and
// default_workspace_url maps to an alias. It re-checks the version, so racing
// processes migrate exactly once and callers may invoke it unconditionally. A
// legacy secret the Keychain won't return stays a placeholder — the workspace
// then reports "missing", same as any dangling placeholder, and heals via the
// usual re-import paths.
func (s *Store) migrateLocked() error {
	file, err := s.readFile()
	if err != nil {
		return err
	}
	if file.Version >= storeVersion {
		return nil
	}

	relabelForV2(file)
	s.hydrateLegacySecrets(file)
	s.deleteLegacyAccounts(file)
	return s.Save(&file.Credentials)
}

// relabelForV2 is the pure in-memory half of the migration: normalize every
// workspace URL, assign each a derived (uniquified) alias, and map the
// version-1 default URL onto the alias of its first match.
func relabelForV2(file *credentialsFile) {
	taken := map[string]bool{}
	for i := range file.Workspaces {
		w := &file.Workspaces[i]
		if n, nerr := normalizeURL(w.URL); nerr == nil {
			w.URL = n
		}
		w.Alias = uniquifyAlias(deriveAlias(*w), func(a string) bool { return taken[a] })
		taken[w.Alias] = true
	}

	if file.LegacyDefaultURL == "" {
		return
	}
	n, err := normalizeURL(file.LegacyDefaultURL)
	if err != nil {
		return
	}
	for _, w := range file.Workspaces {
		if w.URL == n {
			file.DefaultWorkspace = w.Alias
			break
		}
	}
}

// hydrateLegacySecrets fills placeholders from the version-1 URL-keyed
// accounts (and the shared xoxd), so the following Save re-homes each secret
// under its alias account — or keeps it in the file if the Keychain rejects
// the write; never lost either way.
func (s *Store) hydrateLegacySecrets(file *credentialsFile) {
	for i := range file.Workspaces {
		w := &file.Workspaces[i]
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
}

// deleteLegacyAccounts removes the version-1 Keychain accounts once their
// secrets are hydrated.
func (s *Store) deleteLegacyAccounts(file *credentialsFile) {
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
}

// migrateLegacyFile seeds a missing store from the file the TS agent-slack
// maintains. Metadata only, best effort: secrets stay __KEYCHAIN__
// placeholders (the TS Keychain service is different) and refill into our
// service via auth import or the desktop auto-refresh.
func migrateLegacyFile(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	raw, err := os.ReadFile(filepath.Join(xdg.ConfigDir(legacyConfigDirName), "credentials.json"))
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o600)
}
