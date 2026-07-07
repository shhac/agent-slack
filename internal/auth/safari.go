package auth

import (
	"strings"

	browsercookies "github.com/shhac/lib-agent-browsercookies"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

const safariEnableJSHint = "In Safari, enable Develop → Allow JavaScript from Apple Events (Settings → Advanced → Show features for web developers) and allow Automation access when prompted, then re-run: agent-slack auth import-browser safari"

const safariFDAHint = "Safari's cookie store is sandboxed; grant your terminal Full Disk Access (System Settings → Privacy & Security → Full Disk Access), then re-run: agent-slack auth import-browser safari"

// extractFromSafari imports xoxc tokens from a logged-in Slack tab in Safari via
// AppleScript, and the xoxd cookie from Safari's Cookies.binarycookies (macOS
// only). The token read needs Develop-menu "Allow JavaScript from Apple Events"
// plus Automation permission; the cookie read needs Full Disk Access because
// the store lives in Safari's TCC-protected container.
func extractFromSafari() (*Extracted, error) {
	if err := requireMacOS("Safari"); err != nil {
		return nil, err
	}

	teamsRaw, jsDisabled, err := runOsascript(safariTeamsAppleScript())
	if jsDisabled {
		return nil, agenterrors.New("Safari is blocking JavaScript from Apple Events", agenterrors.FixableByHuman).WithHint(safariEnableJSHint)
	}
	if err != nil {
		return nil, agenterrors.New("could not read Slack workspaces from Safari; open Slack in Safari and sign in, then retry", agenterrors.FixableByHuman).WithHint(safariEnableJSHint)
	}
	teams := parseTeamsJSON([]byte(teamsRaw))
	if len(teams) == 0 {
		return nil, errNoWorkspaces("Safari", safariEnableJSHint)
	}

	cookie, cookiesPath, err := safariSlackCookie()
	if err != nil {
		return nil, err
	}

	return &Extracted{
		CookieD: cookie,
		Teams:   teams,
		Source:  map[string]string{"cookies_path": cookiesPath},
	}, nil
}

// safariTeamsAppleScript reads the Slack teams object from a logged-in tab.
// Safari's dictionary uses `do JavaScript … in <tab>`, unlike Chromium's
// `execute … javascript`.
func safariTeamsAppleScript() string {
	js := slackTeamsProbeJS()
	return `
		tell application "Safari"
			repeat with w in windows
				repeat with t in tabs of w
					if URL of t contains "slack.com" then
						return do JavaScript "` + escapeAppleScriptString(js) + `" in t
					end if
				end repeat
			end repeat
			return "{}"
		end tell`
}

// safariSlackCookie reads the Slack `d` cookie from Safari's cookie store via
// the shared library (which owns the Cookies.binarycookies parser and the
// sandboxed-container path resolution), then applies Slack's URL-decoding. A
// permission error means Full Disk Access is missing.
func safariSlackCookie() (cookie, path string, err error) {
	res, lerr := browsercookies.Extract("safari", slackCookieTarget)
	if lerr != nil {
		switch {
		case strings.Contains(lerr.Error(), "permission denied"):
			return "", "", agenterrors.New("could not read Safari's cookie store (permission denied)", agenterrors.FixableByHuman).WithHint(safariFDAHint)
		case strings.Contains(lerr.Error(), "could not find"):
			return "", "", agenterrors.New("could not read Safari's cookie store", agenterrors.FixableByHuman).WithHint(safariFDAHint)
		default:
			return "", "", agenterrors.New("no Slack 'd' cookie found in Safari; open Slack in Safari and sign in, then retry", agenterrors.FixableByHuman)
		}
	}
	return decodeCookieValue(res.Value), res.Source["cookies_path"], nil
}
