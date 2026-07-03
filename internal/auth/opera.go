package auth

import (
	"os"
	"path/filepath"
	"runtime"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// operaBaseDir is the Opera user-data root.
func operaBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "com.operasoftware.Opera"), nil
	case "linux":
		return filepath.Join(home, ".config", "opera"), nil
	case "windows":
		return filepath.Join(windowsAppData(home), "Opera Software", "Opera Stable"), nil
	default:
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "Opera import is not supported on %s", runtime.GOOS)
	}
}

var operaSafeStorageQueries = []safeStorageQuery{
	{service: "Opera Safe Storage"},
	{service: "Chrome Safe Storage"},
	{service: "Chromium Safe Storage"},
}

// locateOpera finds Opera's Local Storage LevelDB and Cookies DB. Opera stores
// data directly under the base dir; newer builds may use a Default profile —
// both are tried.
func locateOpera() (leveldbDir, cookiesDB string, queries []safeStorageQuery, err error) {
	base, err := operaBaseDir()
	if err != nil {
		return "", "", nil, err
	}
	for _, root := range []string{base, filepath.Join(base, "Default")} {
		ldb := filepath.Join(root, "Local Storage", "leveldb")
		if _, statErr := os.Stat(ldb); statErr != nil {
			continue
		}
		cookies := filepath.Join(root, "Network", "Cookies")
		if _, statErr := os.Stat(cookies); statErr != nil {
			cookies = filepath.Join(root, "Cookies")
		}
		return ldb, cookies, operaSafeStorageQueries, nil
	}
	return "", "", nil, agenterrors.New("Opera data not found; open Slack in Opera and sign in, then retry", agenterrors.FixableByHuman)
}
