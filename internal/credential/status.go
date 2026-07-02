package credential

import "github.com/shhac/agent-slack/internal/fslock"

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
	if err := s.migrateIfNeeded(); err != nil {
		return nil, err
	}
	out := map[string]map[string]SecretStatus{}
	raw, _, err := fslock.ReadJSON[Credentials](s.path)
	if err != nil {
		return nil, err
	}
	for i := range raw.Workspaces {
		w := &raw.Workspaces[i]
		st := map[string]SecretStatus{}
		for _, ref := range w.secretRefs() {
			st[ref.name] = s.secretStatus(*ref.value, ref.account)
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
	for _, ref := range ws.secretRefs() {
		if isPlaceholder(*ref.value) {
			missing = append(missing, ref.name)
		}
	}
	return missing
}
