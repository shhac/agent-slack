package auth

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// macOSContainerProtectionMajor is the first macOS (Sonoma) that blocks one
// app from reading another app's sandbox container without Full Disk Access.
const macOSContainerProtectionMajor = 14

// desktopFailure is one Slack Desktop candidate that could not be read.
type desktopFailure struct {
	baseDir string
	err     error
}

// isPrivacyDenial reports whether err is macOS privacy control (TCC) refusing
// access. TCC surfaces as EPERM ("operation not permitted"), unlike ordinary
// file modes, which surface as EACCES ("permission denied").
func isPrivacyDenial(err error) bool {
	return errors.Is(err, syscall.EPERM)
}

func isSandboxContainer(dir string) bool {
	return strings.Contains(filepath.ToSlash(dir), "/Library/Containers/")
}

// desktopPermissionHint explains a macOS privacy denial reading Slack Desktop
// data. It returns "" when no failure was a privacy denial.
func desktopPermissionHint(failures []desktopFailure, macOSMajor int) string {
	denied, inContainer := false, false
	for _, f := range failures {
		if !isPrivacyDenial(f.err) {
			continue
		}
		denied = true
		inContainer = inContainer || isSandboxContainer(f.baseDir)
	}
	if !denied {
		return ""
	}

	cause := "macOS privacy controls blocked reading Slack Desktop's data."
	if inContainer && macOSMajor >= macOSContainerProtectionMajor {
		cause = "This is the Mac App Store build of Slack, which keeps its data in a sandbox container. " +
			"macOS " + strconv.Itoa(macOSMajor) + " (like every macOS since 14 Sonoma) blocks other apps, " +
			"your terminal included, from reading another app's container."
	}
	return strings.Join([]string{
		cause,
		"sudo does not help: this is a macOS privacy setting, not a file permission, and it applies to root too.",
		"Grant the terminal app you run agent-slack from Full Disk Access " +
			"(System Settings → Privacy & Security → Full Disk Access), quit and reopen that terminal, " +
			"then re-run: agent-slack auth import-desktop.",
		"Or skip this by importing from a browser signed into Slack: agent-slack auth import-browser <name>",
	}, " ")
}

func parseMajorVersion(v string) int {
	major, _, _ := strings.Cut(strings.TrimSpace(v), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}
