package auth

import (
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
		raw, err := readSlackLocalConfig(c.leveldbDir)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", c.baseDir, err))
			continue
		}
		cfg, err := parseLocalConfig(raw)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", c.baseDir, err))
			continue
		}
		teams := teamsFromLocalConfig(cfg)
		if len(teams) == 0 {
			failures = append(failures, fmt.Sprintf("%s: no xoxc tokens in localConfig", c.baseDir))
			continue
		}
		cookie, err := extractChromiumCookieD(c.cookiesDB, desktopSafeStorageQueries)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", c.baseDir, err))
			continue
		}
		return &Extracted{
			CookieD: cookie,
			Teams:   teams,
			Source:  map[string]string{"leveldb_path": c.leveldbDir, "cookies_path": c.cookiesDB},
		}, nil
	}

	return nil, agenterrors.Newf(agenterrors.FixableByHuman,
		"could not extract Slack Desktop credentials:\n  - %s", strings.Join(failures, "\n  - "))
}
