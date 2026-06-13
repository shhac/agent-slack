package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// browserSource describes one importable browser and how to extract a
// logged-in Slack session from it. Each source belongs to an extractor family
// (Gecko/file, Chromium/AppleScript, Chromium/file, …); a family is a shared
// mechanism and the source supplies only its per-browser config. Adding a
// browser is a registry entry, not a new command.
type browserSource struct {
	name            string                                   // canonical, lowercase: "chrome", "zen", …
	summary         string                                   // one-line help/hint
	supportsProfile bool                                     // --profile applies (Gecko family)
	extract         func(profile string) (*Extracted, error) // profile ignored when unsupported
}

// browserSources is the registry, in display order.
var browserSources = []browserSource{
	geckoSource("firefox", "Firefox profile on disk (browser need not be running)", firefoxBaseDir),
	geckoSource("zen", "Zen Browser profile on disk (Firefox-based; browser need not be running)", zenBaseDir),
	chromiumAppleSource("chrome", "Google Chrome — reads a logged-in Slack tab (running; macOS)", ExtractFromChrome),
	chromiumAppleSource("brave", "Brave — reads a logged-in Slack tab (running; macOS)", ExtractFromBrave),
	chromiumFileSource("opera", "Opera profile on disk (browser need not be running)", locateOpera),
	webkitSource("safari", "Safari — running tab for tokens + cookie store (macOS; needs Develop-menu JS + Full Disk Access)"),
}

// BrowserInfo is the public description of one supported browser.
type BrowserInfo struct {
	Name            string
	Summary         string
	SupportsProfile bool
}

// SupportedBrowsers lists the importable browsers in display order.
func SupportedBrowsers() []BrowserInfo {
	out := make([]BrowserInfo, len(browserSources))
	for i, s := range browserSources {
		out[i] = BrowserInfo{Name: s.name, Summary: s.summary, SupportsProfile: s.supportsProfile}
	}
	return out
}

// ImportBrowser extracts a Slack session from the named browser. profile is a
// Gecko profile selector, ignored by browsers that don't support it. An
// unknown name returns a FixableByAgent error listing the supported browsers.
func ImportBrowser(name, profile string) (*Extracted, error) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, s := range browserSources {
		if s.name == want {
			return s.extract(profile)
		}
	}
	var names []string
	for _, s := range browserSources {
		names = append(names, s.name)
	}
	return nil, agenterrors.Newf(agenterrors.FixableByAgent, "unknown browser %q", name).
		WithHint("supported browsers: " + strings.Join(names, ", "))
}

// --- family constructors -----------------------------------------------------

// geckoSource builds a Firefox-family source (file-based; supports --profile).
func geckoSource(name, summary string, baseDir func() (string, error)) browserSource {
	display := displayName(name)
	return browserSource{
		name: name, summary: summary, supportsProfile: true,
		extract: func(profile string) (*Extracted, error) {
			return extractFromGecko(display, baseDir, profile)
		},
	}
}

// chromiumAppleSource builds a Chromium-family source that reads a running,
// logged-in browser via AppleScript (macOS). It has no profile concept.
func chromiumAppleSource(name, summary string, extract func() (*Extracted, error)) browserSource {
	return browserSource{
		name: name, summary: summary, supportsProfile: false,
		extract: func(string) (*Extracted, error) { return extract() },
	}
}

// webkitSource builds a WebKit-family source (Safari): tokens from a running
// tab via AppleScript, the cookie from Safari's binarycookies store. No profile
// concept.
func webkitSource(name, summary string) browserSource {
	return browserSource{
		name: name, summary: summary, supportsProfile: false,
		extract: func(string) (*Extracted, error) { return ExtractFromSafari() },
	}
}

// chromiumFileSource builds a Chromium-family source that reads the profile
// from disk (LevelDB tokens + encrypted Cookies DB); the browser need not be
// running. locate resolves the LevelDB dir, Cookies DB, and Safe Storage
// queries for this browser.
func chromiumFileSource(name, summary string, locate func() (leveldbDir, cookiesDB string, queries []safeStorageQuery, err error)) browserSource {
	return browserSource{
		name: name, summary: summary, supportsProfile: false,
		extract: func(string) (*Extracted, error) {
			leveldbDir, cookiesDB, queries, err := locate()
			if err != nil {
				return nil, err
			}
			return extractChromiumFromFiles(leveldbDir, cookiesDB, queries,
				map[string]string{"leveldb_path": leveldbDir, "cookies_path": cookiesDB})
		},
	}
}

// --- per-browser config ------------------------------------------------------

// zenBaseDir is the Zen Browser profile root (Firefox layout).
func zenBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "zen"), nil
	case "linux":
		return filepath.Join(home, ".zen"), nil
	case "windows":
		return filepath.Join(windowsAppData(home), "zen"), nil
	default:
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "Zen import is not supported on %s", runtime.GOOS)
	}
}

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

// displayName capitalizes a source name for human-facing messages.
func displayName(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
