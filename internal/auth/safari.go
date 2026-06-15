package auth

import (
	"os"
	"path/filepath"
	"strings"

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

// safariCookiePaths are the candidate Cookies.binarycookies locations, the
// sandboxed container (modern Safari) first, then the legacy location.
func safariCookiePaths() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []string{
		filepath.Join(home, "Library", "Containers", "com.apple.Safari", "Data", "Library", "Cookies", "Cookies.binarycookies"),
		filepath.Join(home, "Library", "Cookies", "Cookies.binarycookies"),
	}, nil
}

// safariSlackCookie finds the decrypted Slack `d` cookie in Safari's cookie
// store. A permission error means Full Disk Access is missing.
func safariSlackCookie() (cookie, path string, err error) {
	paths, err := safariCookiePaths()
	if err != nil {
		return "", "", err
	}
	readAny := false
	for _, p := range paths {
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			if os.IsPermission(readErr) {
				return "", "", agenterrors.New("could not read Safari's cookie store (permission denied)", agenterrors.FixableByHuman).WithHint(safariFDAHint)
			}
			continue // not present here — try the next location
		}
		readAny = true
		cookies, perr := parseBinaryCookies(data)
		if perr != nil {
			continue
		}
		if cookie, ok := selectSafariSlackCookie(cookies); ok {
			return cookie, p, nil
		}
	}
	if !readAny {
		return "", "", agenterrors.New("could not read Safari's cookie store", agenterrors.FixableByHuman).WithHint(safariFDAHint)
	}
	return "", "", agenterrors.New("no Slack 'd' cookie found in Safari; open Slack in Safari and sign in, then retry", agenterrors.FixableByHuman)
}

// selectSafariSlackCookie returns the decoded Slack `d` cookie value from a
// parsed cookie set: the entry named "d" on a slack.com domain whose value is
// an xoxd- token. The value is URL-decoded like the other browser paths.
func selectSafariSlackCookie(cookies []binaryCookie) (string, bool) {
	for _, c := range cookies {
		if c.Name == "d" && strings.Contains(c.Domain, "slack.com") && strings.HasPrefix(c.Value, "xoxd-") {
			return decodeFirefoxCookie(c.Value), true
		}
	}
	return "", false
}
