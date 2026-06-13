package auth

import (
	"os/exec"
	"strings"
)

// appleScriptJSDisabledMarker is what osascript prints when a Chromium browser
// has "Allow JavaScript from Apple Events" turned off.
const appleScriptJSDisabledMarker = "Executing JavaScript through AppleScript is turned off"

// teamJSONExprs are the in-page expressions tried, in order, to read the Slack
// teams object from a logged-in tab's localStorage.
var teamJSONExprs = []string{
	"JSON.stringify(JSON.parse(localStorage.localConfig_v2).teams)",
	"JSON.stringify(JSON.parse(localStorage.localConfig_v3).teams)",
	"JSON.stringify(JSON.parse(localStorage.getItem('reduxPersist:localConfig'))?.teams || {})",
	"JSON.stringify(window.boot_data?.teams || {})",
}

// slackTeamsProbeJS builds the in-page JavaScript that reads the Slack teams
// object from a logged-in tab's localStorage, trying each known config key in
// order and returning "{}" when none match. Shared by every AppleScript-driven
// browser (the surrounding AppleScript verb differs per browser; this JS does
// not).
func slackTeamsProbeJS() string {
	var tries strings.Builder
	for _, expr := range teamJSONExprs {
		tries.WriteString("try { var v = " + expr + "; if (v && v !== '{}' && v !== 'null') return v; } catch(e) {} ")
	}
	return "(function(){ " + tries.String() + "return '{}'; })()"
}

func teamsAppleScript(appName string) string {
	js := slackTeamsProbeJS()
	return `
		tell application "` + appName + `"
			repeat with w in windows
				repeat with t in tabs of w
					if URL of t contains "slack.com" then
						return execute t javascript "` + escapeAppleScriptString(js) + `"
					end if
				end repeat
			end repeat
			return "{}"
		end tell`
}

func cookieAppleScript(appName string) string {
	js := "document.cookie.split('; ').find(c => c.startsWith('d='))?.split('=')[1] || ''"
	return `
		tell application "` + appName + `"
			repeat with w in windows
				repeat with t in tabs of w
					if URL of t contains "slack.com" then
						return execute t javascript "` + escapeAppleScriptString(js) + `"
					end if
				end repeat
			end repeat
			return ""
		end tell`
}

func escapeAppleScriptString(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// runOsascript executes an AppleScript and returns trimmed stdout. The returned
// jsDisabled flag is true when the browser blocked JavaScript-from-Apple-Events.
func runOsascript(script string) (out string, jsDisabled bool, err error) {
	cmd := exec.Command("osascript", "-e", script)
	stdout, runErr := cmd.Output()
	if runErr != nil {
		stderr := ""
		if ee, ok := runErr.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		if strings.Contains(stderr, appleScriptJSDisabledMarker) || strings.Contains(runErr.Error(), appleScriptJSDisabledMarker) {
			return "", true, runErr
		}
		return "", false, runErr
	}
	return strings.TrimSpace(string(stdout)), false, nil
}
