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
	noProfileSource("chrome", "Google Chrome — reads a logged-in Slack tab (running; macOS)", extractFromChrome),
	noProfileSource("brave", "Brave — reads a logged-in Slack tab (running; macOS)", extractFromBrave),
	chromiumFileSource("opera", "Opera profile on disk (browser need not be running)", locateOpera),
	noProfileSource("safari", "Safari — running tab for tokens + cookie store (macOS; needs Develop-menu JS + Full Disk Access)", extractFromSafari),
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

// noProfileSource builds a source with no --profile concept: extract reads a
// running browser and ignores the profile selector. It covers the Chromium
// AppleScript readers (Chrome, Brave) and Safari, which each pull tokens from a
// live tab plus the browser's own cookie store.
func noProfileSource(name, summary string, extract func() (*Extracted, error)) browserSource {
	return browserSource{
		name: name, summary: summary, supportsProfile: false,
		extract: func(string) (*Extracted, error) { return extract() },
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

// displayName capitalizes a source name for human-facing messages.
func displayName(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}
