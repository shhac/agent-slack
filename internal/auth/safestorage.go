package auth

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	browsercookies "github.com/shhac/lib-agent-browsercookies"
)

// safeStorageQuery is a macOS Keychain lookup for a Chromium "Safe Storage"
// password (service, plus optional account).
type safeStorageQuery struct {
	service string
	account string
}

// slackPlatform builds a browsercookies.Platform whose Keychain closure runs
// Slack's account/attribute-aware secret-store lookups. The library owns the
// snapshot/decrypt mechanism — including Chromium's Linux OSCrypt fallbacks
// (empty passphrase for v11, then "peanuts") — so only the password source is
// injected here.
func slackPlatform(queries []safeStorageQuery) browsercookies.Platform {
	home, _ := os.UserHomeDir()
	return browsercookies.Platform{
		GOOS:   runtime.GOOS,
		Home:   home,
		Getenv: os.Getenv,
		Keychain: func([]string) []string {
			switch runtime.GOOS {
			case "darwin":
				return dedupe(macSafeStoragePasswords(queries))
			case "linux":
				return dedupe(linuxSecretToolPasswords())
			default:
				return nil
			}
		},
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

// linuxSecretToolPasswords reads Slack's Safe Storage password from the login
// keyring via secret-tool, trying the attribute pairs Slack builds are known to
// use. Chromium's OSCrypt fallbacks are added by the library, not here.
func linuxSecretToolPasswords() []string {
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
