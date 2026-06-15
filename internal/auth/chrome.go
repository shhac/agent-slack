package auth

import (
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// extractFromChrome imports xoxc/xoxd from a logged-in Slack tab in Google
// Chrome via AppleScript (macOS only). Chrome serves the xoxd cookie directly
// to the page, so no cookie decryption is needed.
func extractFromChrome() (*Extracted, error) {
	if err := requireMacOS("Chrome"); err != nil {
		return nil, err
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
		return nil, errNoWorkspaces("Chrome", "")
	}

	return &Extracted{CookieD: cookie, Teams: teams}, nil
}
