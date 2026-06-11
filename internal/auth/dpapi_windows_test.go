package auth

// These tests exercise the real DPAPI syscalls, so they only build and run on
// a Windows machine (the _windows_test.go suffix scopes them automatically).

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func dpapiProtect(t *testing.T, plain []byte) []byte {
	t.Helper()
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		t.Fatalf("CryptProtectData: %v", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data))) //nolint:errcheck
	blob := make([]byte, out.Size)
	copy(blob, unsafe.Slice(out.Data, out.Size))
	return blob
}

func TestDPAPIRoundTrip(t *testing.T) {
	secret := []byte("agent-slack dpapi round-trip")
	got, err := dpapiUnprotect(dpapiProtect(t, secret))
	if err != nil || string(got) != string(secret) {
		t.Fatalf("got %q, err %v", got, err)
	}
}

// TestDecryptCookieDPAPIEndToEnd builds a synthetic Chromium profile — a
// Local State with a DPAPI-wrapped AES key and a v10 GCM cookie value — and
// runs the full decryptCookieDPAPI path against it.
func TestDecryptCookieDPAPIEndToEnd(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "Network"), 0o700); err != nil {
		t.Fatal(err)
	}
	encryptedKey := base64.StdEncoding.EncodeToString(append([]byte("DPAPI"), dpapiProtect(t, key)...))
	state, err := json.Marshal(map[string]any{
		"os_crypt": map[string]any{"encrypted_key": encryptedKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "Local State"), state, 0o600); err != nil {
		t.Fatal(err)
	}

	encrypted := gcmEncryptCookie(t, key, []byte("d=xoxd-windows%2Fsecret;"))
	got, err := decryptCookieDPAPI(filepath.Join(base, "Network", "Cookies"), encrypted)
	if err != nil || got != "xoxd-windows/secret" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

// TestExtractFromSlackDesktopOnWindows is a live integration check against the
// local Slack Desktop install; it skips when no Slack data is present.
func TestExtractFromSlackDesktopOnWindows(t *testing.T) {
	if _, err := slackDesktopCandidates(); err != nil {
		t.Skipf("no Slack Desktop data on this machine: %v", err)
	}
	extracted, err := ExtractFromSlackDesktop()
	if err != nil {
		t.Fatalf("ExtractFromSlackDesktop: %v", err)
	}
	if len(extracted.Teams) == 0 || extracted.CookieD == "" {
		t.Fatalf("extraction returned no usable credentials: %d teams", len(extracted.Teams))
	}
}
