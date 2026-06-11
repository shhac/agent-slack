package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func localStateJSON(t *testing.T, encryptedKey string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"os_crypt": map[string]any{"encrypted_key": encryptedKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseLocalStateKey(t *testing.T) {
	keyBlob := []byte("wrapped-key-bytes")
	dpapi := base64.StdEncoding.EncodeToString(append([]byte("DPAPI"), keyBlob...))

	got, err := parseLocalStateKey(localStateJSON(t, dpapi))
	if err != nil || string(got) != string(keyBlob) {
		t.Errorf("DPAPI key = %q, err %v", got, err)
	}

	appBound := base64.StdEncoding.EncodeToString(append([]byte("APPB"), keyBlob...))
	if _, err := parseLocalStateKey(localStateJSON(t, appBound)); err == nil || !strings.Contains(err.Error(), "app-bound") {
		t.Errorf("APPB err = %v", err)
	}

	if _, err := parseLocalStateKey([]byte(`{"os_crypt":{}}`)); err == nil || !strings.Contains(err.Error(), "no os_crypt.encrypted_key") {
		t.Errorf("missing key err = %v", err)
	}
	if _, err := parseLocalStateKey([]byte(`not json`)); err == nil {
		t.Error("bad JSON should error")
	}
	if _, err := parseLocalStateKey(localStateJSON(t, "%%%not-base64")); err == nil {
		t.Error("bad base64 should error")
	}
	plain := base64.StdEncoding.EncodeToString(keyBlob)
	if _, err := parseLocalStateKey(localStateJSON(t, plain)); err == nil || !strings.Contains(err.Error(), "unrecognized") {
		t.Errorf("unknown prefix err = %v", err)
	}
}

// gcmEncryptCookie builds a "v10" Windows cookie value around plaintext.
func gcmEncryptCookie(t *testing.T, key, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	out := append([]byte("v10"), nonce...)
	return aead.Seal(out, nonce, plaintext, nil)
}

func TestDecryptChromiumCookieGCM(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	encrypted := gcmEncryptCookie(t, key, []byte("xoxd-test%2Fvalue"))
	got, err := decryptChromiumCookieGCM(encrypted, key)
	if err != nil || got != "xoxd-test/value" {
		t.Errorf("got %q, err %v", got, err)
	}

	// Chromium m130+ prepends SHA-256(host_key) to the plaintext.
	hash := sha256.Sum256([]byte(".slack.com"))
	prefixed := gcmEncryptCookie(t, key, append(hash[:], []byte("xoxd-prefixed")...))
	got, err = decryptChromiumCookieGCM(prefixed, key)
	if err != nil || got != "xoxd-prefixed" {
		t.Errorf("host-hash-prefixed got %q, err %v", got, err)
	}

	wrongKey := make([]byte, 32)
	if _, err := decryptChromiumCookieGCM(encrypted, wrongKey); err == nil {
		t.Error("wrong key should fail authentication")
	}
	if _, err := decryptChromiumCookieGCM([]byte("v10short"), key); err == nil {
		t.Error("short input should error")
	}
}

func TestFindLocalState(t *testing.T) {
	base := t.TempDir()
	network := filepath.Join(base, "Network")
	if err := os.MkdirAll(network, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "Local State"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Cookies under Network/ — Local State is one level up.
	got, err := findLocalState(filepath.Join(network, "Cookies"))
	if err != nil || got != filepath.Join(base, "Local State") {
		t.Errorf("got %q, err %v", got, err)
	}
	// Cookies directly in the base dir.
	got, err = findLocalState(filepath.Join(base, "Cookies"))
	if err != nil || got != filepath.Join(base, "Local State") {
		t.Errorf("got %q, err %v", got, err)
	}
	if _, err := findLocalState(filepath.Join(t.TempDir(), "Network", "Cookies")); err == nil {
		t.Error("missing Local State should error")
	}
}
