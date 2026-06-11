package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"testing"
)

// encryptVector builds a Chromium-style encrypted cookie body (without the v10
// prefix) for the given plaintext, so the decryptor can be tested end-to-end
// with a known password/iteration count.
func encryptVector(t *testing.T, plaintext, password string, iterations int) []byte {
	t.Helper()
	key := pbkdf2SHA1([]byte(password), []byte("saltysalt"), iterations, 16)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7Pad([]byte(plaintext), aes.BlockSize)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, bytes16Spaces()).CryptBlocks(out, padded)
	return out
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func TestDecryptChromiumCookieRoundTrip(t *testing.T) {
	const password = "s3cr3t-safe-storage"
	const iterations = 1003
	enc := encryptVector(t, "xoxd-RealCookieValue123", password, iterations)

	got, err := decryptChromiumCookie(enc, password, iterations)
	if err != nil {
		t.Fatal(err)
	}
	if got != "xoxd-RealCookieValue123" {
		t.Errorf("decrypted = %q", got)
	}
}

func TestDecryptChromiumCookieURLDecodes(t *testing.T) {
	const password = "pw"
	enc := encryptVector(t, "leading-junk xoxd-Ab%2FCd", password, 1003)
	got, err := decryptChromiumCookie(enc, password, 1003)
	if err != nil {
		t.Fatal(err)
	}
	if got != "xoxd-Ab/Cd" {
		t.Errorf("decrypted = %q, want xoxd-Ab/Cd", got)
	}
}

func TestDecryptChromiumCookieWrongPassword(t *testing.T) {
	enc := encryptVector(t, "xoxd-secret", "right", 1003)
	// Wrong password may fail to unpad or simply not contain the marker; either
	// way it must error rather than return a bogus token.
	if _, err := decryptChromiumCookie(enc, "wrong", 1003); err == nil {
		t.Error("expected error with wrong password")
	}
}

func TestDecryptChromiumCookieGuards(t *testing.T) {
	if _, err := decryptChromiumCookie(nil, "pw", 1003); err == nil {
		t.Error("expected error on empty data")
	}
	if _, err := decryptChromiumCookie([]byte("abc"), "pw", 0); err == nil {
		t.Error("expected error on iterations < 1")
	}
	if _, err := decryptChromiumCookie([]byte("not-block-aligned"), "pw", 1003); err == nil {
		t.Error("expected error on non-block-aligned data")
	}
}

func TestPBKDF2KnownVector(t *testing.T) {
	// RFC 6070 PBKDF2-HMAC-SHA1: P="password", S="salt", c=1, dkLen=20.
	got := pbkdf2SHA1([]byte("password"), []byte("salt"), 1, 20)
	want := []byte{
		0x0c, 0x60, 0xc8, 0x0f, 0x96, 0x1f, 0x0e, 0x71, 0xf3, 0xa9,
		0xb5, 0x24, 0xaf, 0x60, 0x12, 0x06, 0x2f, 0xe0, 0x37, 0xa6,
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %#x, want %#x", i, got[i], want[i])
		}
	}
}
