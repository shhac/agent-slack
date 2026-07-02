package credential

import (
	"encoding/json"
	"os"
)

// SecretStatus says where one stored secret lives. "missing" means the file
// holds a Keychain placeholder whose backing entry is gone — e.g. seeded by
// the legacy-file migration and never refilled.
type SecretStatus string

const (
	SecretInKeychain SecretStatus = "keychain"
	SecretInFile     SecretStatus = "file"
	SecretMissing    SecretStatus = "missing"
)

// SecretStatuses reports, per workspace alias, where each secret the auth
// type needs lives ("token" for standard auth; "xoxc"/"xoxd" for browser
// auth). It reads the raw file (placeholders intact) and probes the Keychain
// without returning any secret material. A version-1 file is migrated first
// so the per-alias accounts exist to probe.
func (s *Store) SecretStatuses() (map[string]map[string]SecretStatus, error) {
	if err := s.ensureMigrated(); err != nil {
		return nil, err
	}
	out := map[string]map[string]SecretStatus{}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	raw := &Credentials{}
	if err := json.Unmarshal(data, raw); err != nil {
		return out, nil // corrupt file reads as empty, matching Load
	}
	for _, w := range raw.Workspaces {
		st := map[string]SecretStatus{}
		switch w.Auth.Type {
		case AuthBrowser:
			st["xoxc"] = s.secretStatus(w.Auth.XOXC, xoxcAccount(w.Alias))
			st["xoxd"] = s.secretStatus(w.Auth.XOXD, xoxdAccount(w.Alias))
		default:
			st["token"] = s.secretStatus(w.Auth.Token, tokenAccount(w.Alias))
		}
		out[w.Alias] = st
	}
	return out, nil
}

func (s *Store) secretStatus(rawValue, account string) SecretStatus {
	if !isPlaceholder(rawValue) {
		return SecretInFile
	}
	if _, ok := s.kc.Get(account); ok {
		return SecretInKeychain
	}
	return SecretMissing
}

// MissingSecrets lists the secret fields a hydrated (Load-ed) workspace still
// lacks — empty values or literal placeholders that found no Keychain entry.
// A non-empty result means the workspace cannot authenticate as stored.
func MissingSecrets(ws Workspace) []string {
	var missing []string
	switch ws.Auth.Type {
	case AuthBrowser:
		if isPlaceholder(ws.Auth.XOXC) {
			missing = append(missing, "xoxc")
		}
		if isPlaceholder(ws.Auth.XOXD) {
			missing = append(missing, "xoxd")
		}
	default:
		if isPlaceholder(ws.Auth.Token) {
			missing = append(missing, "token")
		}
	}
	return missing
}
