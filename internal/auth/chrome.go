package auth

import (
	"runtime"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// extractFromChrome imports xoxc/xoxd from a logged-in Slack tab in Google
// Chrome via AppleScript (macOS only). Chrome serves the xoxd cookie directly
// to the page, so no cookie decryption is needed.
func extractFromChrome() (*Extracted, error) {
	if runtime.GOOS != "darwin" {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "Chrome import is only supported on macOS, not %s", runtime.GOOS)
	}

	cookie, _, err := runOsascript(cookieAppleScript("Google Chrome"))
	if err != nil || !strings.HasPrefix(cookie, "xoxd-") {
		return nil, agenterrors.New("could not read the Slack cookie from Chrome; open Slack in Chrome and sign in, then retry", agenterrors.FixableByHuman)
	}

	teamsRaw, _, err := runOsascript(teamsAppleScript("Google Chrome"))
	if err != nil {
		return nil, agenterrors.New("could not read Slack workspaces from Chrome; open Slack in Chrome and sign in, then retry", agenterrors.FixableByHuman)
	}
	teams := parseTeamsJSON([]byte(teamsRaw))
	if len(teams) == 0 {
		return nil, agenterrors.New("no Slack workspaces found in the open Chrome tab", agenterrors.FixableByHuman)
	}

	return &Extracted{CookieD: cookie, Teams: teams}, nil
}
