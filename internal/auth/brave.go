package auth

import (
	"os"
	"path/filepath"
	"runtime"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

const braveJSDisabledHint = "Enable it in Brave: View → Developer → Allow JavaScript from Apple Events, then re-run: agent-slack auth import-brave"

var braveSafeStorageQueries = []safeStorageQuery{
	{service: "Brave Safe Storage"},
	{service: "Brave Browser Safe Storage"},
	{service: "Chrome Safe Storage"},
	{service: "Chromium Safe Storage"},
}

// ExtractFromBrave imports xoxc tokens from a logged-in Slack tab in Brave via
// AppleScript and the xoxd cookie from Brave's encrypted Cookies DB (macOS
// only). Reading the tab requires Brave's "Allow JavaScript from Apple Events".
func ExtractFromBrave() (*Extracted, error) {
	if runtime.GOOS != "darwin" {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "Brave import is only supported on macOS, not %s", runtime.GOOS)
	}

	teamsRaw, jsDisabled, err := runOsascript(teamsAppleScript("Brave Browser"))
	if jsDisabled {
		return nil, agenterrors.New("Brave is blocking JavaScript from Apple Events", agenterrors.FixableByHuman).WithHint(braveJSDisabledHint)
	}
	if err != nil {
		return nil, agenterrors.New("could not read Slack workspaces from Brave; open Slack in Brave and sign in, then retry", agenterrors.FixableByHuman)
	}
	teams := parseTeamsJSON([]byte(teamsRaw))
	if len(teams) == 0 {
		return nil, agenterrors.New("no Slack workspaces found in the open Brave tab", agenterrors.FixableByHuman)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cookiesDB := filepath.Join(home, "Library", "Application Support", "BraveSoftware", "Brave-Browser", "Default", "Cookies")
	cookie, err := extractChromiumCookieD(cookiesDB, braveSafeStorageQueries)
	if err != nil {
		return nil, agenterrors.Wrap(err, agenterrors.FixableByHuman)
	}

	return &Extracted{CookieD: cookie, Teams: teams}, nil
}
