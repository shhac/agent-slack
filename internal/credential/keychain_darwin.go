//go:build darwin

package credential

import (
	"os/exec"
	"strings"
)

// securityKeychain implements Keychain via the macOS `security` CLI. The
// command runner is a field so tests can drive it without a real Keychain.
type securityKeychain struct {
	run func(args ...string) (string, error)
}

func defaultKeychain() Keychain {
	return &securityKeychain{run: runSecurity}
}

func runSecurity(args ...string) (string, error) {
	out, err := exec.Command("security", args...).Output()
	return string(out), err
}

func (k *securityKeychain) Get(account string) (string, bool) {
	out, err := k.run("find-generic-password", "-s", keychainService, "-a", account, "-w")
	if err != nil {
		return "", false
	}
	v := strings.TrimRight(out, "\n")
	if v == "" {
		return "", false
	}
	return v, true
}

func (k *securityKeychain) Set(account, value string) bool {
	// -U updates an existing item in place; delete first so a stale item with
	// different attributes can't shadow the write.
	_, _ = k.run("delete-generic-password", "-s", keychainService, "-a", account)
	_, err := k.run("add-generic-password", "-s", keychainService, "-a", account, "-w", value, "-U")
	return err == nil
}

func (k *securityKeychain) Delete(account string) {
	_, _ = k.run("delete-generic-password", "-s", keychainService, "-a", account)
}

func (k *securityKeychain) Available() bool { return true }
