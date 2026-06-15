package auth

import (
	"runtime"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// requireMacOS guards the macOS-only browser extractors (Chrome/Brave/Safari
// read a live tab via AppleScript). Firefox uses the cross-platform Gecko path
// and does not call this.
func requireMacOS(browser string) error {
	if runtime.GOOS == "darwin" {
		return nil
	}
	return agenterrors.Newf(agenterrors.FixableByAgent, "%s import is only supported on macOS, not %s", browser, runtime.GOOS)
}

// errNoWorkspaces is the shared "no Slack tab open" error. Pass a non-empty hint
// for browsers (e.g. Safari) that have a setup nuance worth surfacing.
func errNoWorkspaces(browser, hint string) error {
	e := agenterrors.Newf(agenterrors.FixableByHuman, "no Slack workspaces found in the open %s tab", browser)
	if hint != "" {
		return e.WithHint(hint)
	}
	return e
}
