package auth

import (
	"errors"
	"runtime"
	"strings"
)

// extractChromiumCookieD reads the Slack `d` cookie from a Chromium-family
// Cookies SQLite database and returns the decrypted xoxd- value. macQueries are
// the macOS Keychain Safe Storage lookups to try for the decryption password.
func extractChromiumCookieD(cookiesPath string, macQueries []safeStorageQuery) (string, error) {
	copyPath, cleanup, err := copySqliteForRead(cookiesPath)
	if err != nil {
		return "", err
	}
	defer cleanup()

	rows, err := queryReadonlySqlite(copyPath,
		"select host_key, name, value, encrypted_value from cookies where name = 'd' and host_key like '%slack.com' order by length(encrypted_value) desc")
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", errors.New("no Slack 'd' cookie found")
	}

	row := rows[0]
	if v := rowString(row, "value"); strings.HasPrefix(v, "xoxd-") {
		return v, nil
	}

	encrypted := rowBytes(row, "encrypted_value")
	if len(encrypted) == 0 {
		return "", errors.New("slack 'd' cookie had no encrypted_value")
	}

	if runtime.GOOS == "windows" {
		// Windows wraps the key with DPAPI instead of a Safe Storage password;
		// Local State is found relative to the ORIGINAL path, not the temp copy.
		return decryptCookieDPAPI(cookiesPath, encrypted)
	}

	prefix := ""
	if len(encrypted) >= 3 {
		prefix = string(encrypted[:3])
	}
	data := encrypted
	if prefix == "v10" || prefix == "v11" {
		data = encrypted[3:]
	}

	passwords := safeStoragePasswords(macQueries, prefix)
	if len(passwords) == 0 {
		return "", errors.New("could not read a Safe Storage password from the OS keychain")
	}
	for _, pw := range passwords {
		if token, err := decryptChromiumCookie(data, pw, chromiumIterations()); err == nil {
			return token, nil
		}
	}
	return "", errors.New("could not decrypt the Slack 'd' cookie with any Safe Storage password")
}
