//go:build darwin

package credential

import (
	"errors"
	"testing"
)

// fakeSecurity simulates the macOS `security` CLI over an in-memory map so the
// darwin keychain code path is exercised without touching the real Keychain.
type fakeSecurity struct {
	items map[string]string // account -> value
}

func (f *fakeSecurity) run(args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("no args")
	}
	account := flagValue(args, "-a")
	switch args[0] {
	case "find-generic-password":
		v, ok := f.items[account]
		if !ok {
			return "", errors.New("not found")
		}
		return v + "\n", nil
	case "add-generic-password":
		f.items[account] = flagValue(args, "-w")
		return "", nil
	case "delete-generic-password":
		delete(f.items, account)
		return "", nil
	}
	return "", errors.New("unknown command")
}

func flagValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func TestSecurityKeychainRoundTrip(t *testing.T) {
	f := &fakeSecurity{items: map[string]string{}}
	kc := &securityKeychain{run: f.run}

	if _, ok := kc.Get("xoxd"); ok {
		t.Error("expected miss before set")
	}
	if !kc.Set("xoxd", "xoxd-secret") {
		t.Fatal("Set should succeed")
	}
	v, ok := kc.Get("xoxd")
	if !ok || v != "xoxd-secret" {
		t.Errorf("Get = %q, ok=%v; want xoxd-secret", v, ok)
	}
	kc.Delete("xoxd")
	if _, ok := kc.Get("xoxd"); ok {
		t.Error("expected miss after delete")
	}
	if !kc.Available() {
		t.Error("darwin keychain should report available")
	}
}
