package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"database/sql"
	"path/filepath"
	"testing"

	browsercookies "github.com/shhac/lib-agent-browsercookies"
	_ "modernc.org/sqlite"
)

// encryptV10Cookie builds a Chromium "v10" AES-128-CBC cookie body the way macOS
// stores it, so the wiring test drives the real decrypt path with a known
// password. Mirrors the library's scheme (PBKDF2-SHA1 saltysalt, 16-space IV).
func encryptV10Cookie(t *testing.T, plaintext, password string) []byte {
	t.Helper()
	key, err := pbkdf2.Key(sha1.New, password, []byte("saltysalt"), 1003, 16)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append([]byte(plaintext), make([]byte, pad)...)
	for i := len(plaintext); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	iv := make([]byte, 16)
	for i := range iv {
		iv[i] = ' '
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return append([]byte("v10"), out...)
}

func writeEncryptedCookieDB(t *testing.T, dir string, encrypted []byte) string {
	t.Helper()
	path := filepath.Join(dir, "Cookies")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cookies (host_key, name, value, encrypted_value) VALUES ('.slack.com', 'd', '', ?)`,
		encrypted); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestExtractChromiumCookieDEncrypted proves the full wiring: the injected Slack
// keychain supplies the Safe Storage password, the library decrypts the v10
// cookie, and xoxdFromPlain recovers the URL-decoded token.
func TestExtractChromiumCookieDEncrypted(t *testing.T) {
	const password = "s3cr3t-safe-storage"
	enc := encryptV10Cookie(t, "xoxd-Ab%2FCd", password)
	dbPath := writeEncryptedCookieDB(t, t.TempDir(), enc)

	fakeKeychain := browsercookies.Platform{
		GOOS:     "darwin",
		Home:     t.TempDir(),
		Getenv:   func(string) string { return "" },
		Keychain: func([]string) []string { return []string{"wrong-pw", password} },
	}

	got, err := extractChromiumCookieD(dbPath, nil, browsercookies.WithPlatform(fakeKeychain))
	if err != nil {
		t.Fatal(err)
	}
	if got != "xoxd-Ab/Cd" {
		t.Errorf("decrypted cookie = %q, want xoxd-Ab/Cd", got)
	}
}
