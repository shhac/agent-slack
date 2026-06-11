package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Windows Chromium cookie encryption: an AES-256-GCM key lives in the
// profile's "Local State" JSON, wrapped with DPAPI; each "v10" cookie value is
// nonce(12) || ciphertext || tag(16) after the 3-byte version prefix. Only the
// DPAPI unwrap itself (dpapiUnprotect) is Windows-only; everything here is
// pure and unit-tested on every platform.

// decryptCookieDPAPI decrypts a Chromium cookie encrypted_value using the
// Windows scheme. "v10"-prefixed values use the wrapped AES-GCM key; older
// unprefixed values are DPAPI blobs directly. cookiesPath locates the
// Local State file, which sits in the profile base dir above the Cookies DB.
func decryptCookieDPAPI(cookiesPath string, encrypted []byte) (string, error) {
	if bytes.HasPrefix(encrypted, []byte("v10")) {
		key, err := windowsCookieKey(cookiesPath)
		if err != nil {
			return "", err
		}
		return decryptChromiumCookieGCM(encrypted, key)
	}
	plain, err := dpapiUnprotect(encrypted)
	if err != nil {
		return "", err
	}
	return xoxdFromPlain(plain)
}

func windowsCookieKey(cookiesPath string) ([]byte, error) {
	statePath, err := findLocalState(cookiesPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}
	blob, err := parseLocalStateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", statePath, err)
	}
	return dpapiUnprotect(blob)
}

// findLocalState walks up from the Cookies DB looking for "Local State":
// cookies live at <base>/Network/Cookies or <base>/Cookies, and Local State
// at <base>/Local State.
func findLocalState(cookiesPath string) (string, error) {
	dir := filepath.Dir(cookiesPath)
	for range 3 {
		p := filepath.Join(dir, "Local State")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no Local State file found near %s", cookiesPath)
}

// parseLocalStateKey returns the DPAPI-wrapped AES key blob from a Chromium
// "Local State" file (os_crypt.encrypted_key, base64 with a "DPAPI" prefix).
func parseLocalStateKey(raw []byte) ([]byte, error) {
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("parse Local State: %w", err)
	}
	if state.OSCrypt.EncryptedKey == "" {
		return nil, errors.New("no os_crypt.encrypted_key in Local State")
	}
	blob, err := base64.StdEncoding.DecodeString(state.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted_key: %w", err)
	}
	switch {
	case bytes.HasPrefix(blob, []byte("DPAPI")):
		return blob[len("DPAPI"):], nil
	case bytes.HasPrefix(blob, []byte("APPB")):
		return nil, errors.New("cookie key uses app-bound encryption (Chrome 127+), which cannot be unwrapped from user context; use 'auth parse-curl' instead")
	default:
		return nil, errors.New("unrecognized encrypted_key format in Local State")
	}
}

// decryptChromiumCookieGCM decrypts a "v10" Windows cookie value. Newer
// Chromium prepends a 32-byte SHA-256 of the host key to the plaintext; the
// xoxd token scan in xoxdFromPlain is indifferent to that prefix.
func decryptChromiumCookieGCM(encrypted, key []byte) (string, error) {
	const prefixLen, nonceLen, tagLen = 3, 12, 16
	if len(encrypted) < prefixLen+nonceLen+tagLen {
		return "", errors.New("encrypted cookie too short for AES-GCM")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := encrypted[prefixLen : prefixLen+nonceLen]
	plain, err := aead.Open(nil, nonce, encrypted[prefixLen+nonceLen:], nil)
	if err != nil {
		return "", fmt.Errorf("AES-GCM decrypt: %w", err)
	}
	return xoxdFromPlain(plain)
}
