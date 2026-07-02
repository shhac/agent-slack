package credential

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/shhac/lib-agent-cli/xdg"
)

const (
	// configDirName deviates from the family's plain-tool-name convention
	// because the TS stablyai-agent-slack already owns
	// ~/.config/agent-slack/credentials.json (same filename, different
	// Keychain service) — sharing the file would mean two writers.
	configDirName = "app.paulie.agent-slack"
	// legacyConfigDirName is the TS tool's directory; read once for
	// migration, never written.
	legacyConfigDirName = "agent-slack"
)

// defaultPath follows the agent-* family convention (per lin):
// $XDG_CONFIG_HOME, else ~/.config — on every platform, deliberately not
// os.UserConfigDir (which would scatter macOS state into
// ~/Library/Application Support). xdg.ConfigDir applies exactly that rule.
func defaultPath() (string, error) {
	if env := os.Getenv("AGENT_SLACK_CREDENTIALS"); env != "" {
		return env, nil
	}
	return filepath.Join(xdg.ConfigDir(configDirName), "credentials.json"), nil
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
