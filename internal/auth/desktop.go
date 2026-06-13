package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// slackDesktopPaths is one candidate Slack Desktop data location.
type slackDesktopPaths struct {
	leveldbDir string
	cookiesDB  string
	baseDir    string
}

func slackDesktopCandidates() ([]slackDesktopPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var baseDirs []string
	switch runtime.GOOS {
	case "darwin":
		baseDirs = []string{
			filepath.Join(home, "Library", "Application Support", "Slack"),
			filepath.Join(home, "Library", "Containers", "com.tinyspeck.slackmacgap", "Data", "Library", "Application Support", "Slack"),
		}
	case "linux":
		baseDirs = []string{
			filepath.Join(home, ".var", "app", "com.slack.Slack", "config", "Slack"),
			filepath.Join(home, ".config", "Slack"),
		}
	case "windows":
		baseDirs = []string{filepath.Join(windowsAppData(home), "Slack")}
	default:
		return nil, agenterrors.Newf(agenterrors.FixableByAgent,
			"Slack Desktop import is not supported on %s", runtime.GOOS)
	}

	var out []slackDesktopPaths
	for _, base := range baseDirs {
		leveldbDir := filepath.Join(base, "Local Storage", "leveldb")
		if _, err := os.Stat(leveldbDir); err != nil {
			continue
		}
		cookiesDB := filepath.Join(base, "Network", "Cookies")
		if _, err := os.Stat(cookiesDB); err != nil {
			cookiesDB = filepath.Join(base, "Cookies")
		}
		out = append(out, slackDesktopPaths{leveldbDir: leveldbDir, cookiesDB: cookiesDB, baseDir: base})
	}
	if len(out) == 0 {
		return nil, agenterrors.New("Slack Desktop data not found; open Slack Desktop and sign in, then retry", agenterrors.FixableByHuman)
	}
	return out, nil
}

// desktopSafeStorageQueries are the macOS Keychain lookups for the Slack
// Desktop cookie-encryption password.
var desktopSafeStorageQueries = []safeStorageQuery{
	{service: "Slack Safe Storage", account: "Slack Key"},
	{service: "Slack Safe Storage", account: "Slack App Store Key"},
	{service: "Slack Safe Storage"},
	{service: "Chrome Safe Storage"},
	{service: "Chromium Safe Storage"},
}

// ExtractFromSlackDesktop imports xoxc tokens and the xoxd cookie from local
// Slack Desktop data. Slack does not need to be quit.
func ExtractFromSlackDesktop() (*Extracted, error) {
	candidates, err := slackDesktopCandidates()
	if err != nil {
		return nil, err
	}

	var failures []string
	for _, c := range candidates {
		extracted, err := extractChromiumFromFiles(c.leveldbDir, c.cookiesDB, desktopSafeStorageQueries,
			map[string]string{"leveldb_path": c.leveldbDir, "cookies_path": c.cookiesDB})
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", c.baseDir, err))
			continue
		}
		return extracted, nil
	}

	return nil, agenterrors.Newf(agenterrors.FixableByHuman,
		"could not extract Slack Desktop credentials:\n  - %s", strings.Join(failures, "\n  - "))
}

// extractChromiumFromFiles reads Slack credentials from a Chromium-family
// profile on disk: the localConfig (xoxc tokens) from its Local Storage
// LevelDB, and the xoxd cookie from its encrypted Cookies SQLite. Shared by
// Slack Desktop and file-based browser sources (Opera, …). The caller owns
// multi-candidate iteration and failure aggregation; source labels this
// result's origin. Returns a plain error so callers can wrap or accumulate it.
func extractChromiumFromFiles(leveldbDir, cookiesDB string, queries []safeStorageQuery, source map[string]string) (*Extracted, error) {
	raw, err := readSlackLocalConfig(leveldbDir)
	if err != nil {
		return nil, err
	}
	cfg, err := parseLocalConfig(raw)
	if err != nil {
		return nil, err
	}
	teams := teamsFromLocalConfig(cfg)
	if len(teams) == 0 {
		return nil, errors.New("no xoxc tokens in localConfig")
	}
	cookie, err := extractChromiumCookieD(cookiesDB, queries)
	if err != nil {
		return nil, err
	}
	return &Extracted{CookieD: cookie, Teams: teams, Source: source}, nil
}
