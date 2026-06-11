package auth

import (
	"os/exec"
	"runtime"
	"strings"
)

// safeStorageQuery is a macOS Keychain lookup for a Chromium "Safe Storage"
// password (service, plus optional account).
type safeStorageQuery struct {
	service string
	account string
}

// safeStoragePasswords returns candidate Chromium cookie-encryption passwords
// from the OS secret store. On macOS it reads the given Keychain queries via
// `security`; on Linux it tries `secret-tool` plus the well-known Chromium
// fallbacks. The caller tries each password until one decrypts the cookie.
func safeStoragePasswords(macQueries []safeStorageQuery, prefix string) []string {
	switch runtime.GOOS {
	case "darwin":
		return dedupe(macSafeStoragePasswords(macQueries))
	case "linux":
		// The Linux fallbacks deliberately include an empty password (Chromium
		// OSCrypt v11), so we dedupe without dropping it.
		return dedupe(linuxSafeStoragePasswords(prefix))
	default:
		return nil
	}
}

func macSafeStoragePasswords(queries []safeStorageQuery) []string {
	var out []string
	for _, q := range queries {
		args := []string{"find-generic-password", "-w", "-s", q.service}
		if q.account != "" {
			args = append(args, "-a", q.account)
		}
		if v, err := exec.Command("security", args...).Output(); err == nil {
			if s := strings.TrimRight(string(v), "\n"); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func linuxSafeStoragePasswords(prefix string) []string {
	var out []string
	attrs := [][]string{
		{"application", "com.slack.Slack"},
		{"application", "Slack"},
		{"application", "slack"},
		{"service", "Slack Safe Storage"},
	}
	for _, pair := range attrs {
		if v, err := exec.Command("secret-tool", append([]string{"lookup"}, pair...)...).Output(); err == nil {
			if s := strings.TrimRight(string(v), "\n"); s != "" {
				out = append(out, s)
			}
		}
	}
	// Chromium Linux OSCrypt fallbacks (see os_crypt_linux.cc).
	if prefix == "v11" {
		out = append(out, "")
	}
	out = append(out, "peanuts")
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// chromiumIterations returns the PBKDF2 iteration count for the current OS.
func chromiumIterations() int {
	if runtime.GOOS == "linux" {
		return 1
	}
	return 1003
}
