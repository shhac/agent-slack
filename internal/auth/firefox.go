package auth

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

type firefoxProfile struct {
	name      string
	path      string
	isDefault bool
}

func firefoxBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Firefox"), nil
	case "linux":
		return filepath.Join(home, ".mozilla", "firefox"), nil
	default:
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "Firefox import is not supported on %s", runtime.GOOS)
	}
}

// parseProfilesIni parses Firefox's profiles.ini into profile candidates,
// resolving relative paths against baseDir and honoring both per-[Profile]
// Default=1 and [Install] Default=<path> markers.
func parseProfilesIni(raw, baseDir string) []firefoxProfile {
	type entry struct {
		name       string
		path       string
		isRelative bool
		isDefault  bool
	}
	var entries []entry
	installDefaults := map[string]bool{}

	var section string
	var cur *entry
	flush := func() {
		if cur != nil {
			entries = append(entries, *cur)
			cur = nil
		}
	}

	for _, lineRaw := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(strings.TrimRight(lineRaw, "\r"))
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section = line[1 : len(line)-1]
			if strings.HasPrefix(section, "Profile") {
				cur = &entry{isRelative: true}
			}
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx == -1 {
			continue
		}
		key, value := strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:])
		if strings.HasPrefix(section, "Profile") && cur != nil {
			switch key {
			case "Name":
				cur.name = value
			case "Path":
				cur.path = value
			case "IsRelative":
				cur.isRelative = value != "0"
			case "Default":
				cur.isDefault = value == "1"
			}
			continue
		}
		if strings.HasPrefix(section, "Install") && key == "Default" && value != "" {
			installDefaults[value] = true
		}
	}
	flush()

	var profiles []firefoxProfile
	for _, e := range entries {
		if e.path == "" {
			continue
		}
		path := e.path
		if e.isRelative {
			path = filepath.Join(baseDir, e.path)
		}
		profiles = append(profiles, firefoxProfile{
			name:      e.name,
			path:      path,
			isDefault: e.isDefault || installDefaults[e.path],
		})
	}
	return profiles
}

func listFirefoxProfiles() ([]firefoxProfile, error) {
	baseDir, err := firefoxBaseDir()
	if err != nil {
		return nil, err
	}
	var candidates []firefoxProfile
	if raw, err := os.ReadFile(filepath.Join(baseDir, "profiles.ini")); err == nil {
		candidates = append(candidates, parseProfilesIni(string(raw), baseDir)...)
	}

	scanDir := baseDir
	if runtime.GOOS == "darwin" {
		scanDir = filepath.Join(baseDir, "Profiles")
	}
	if entries, err := os.ReadDir(scanDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(scanDir, e.Name())
			seen := false
			for _, c := range candidates {
				if c.path == p {
					seen = true
					break
				}
			}
			if !seen {
				candidates = append(candidates, firefoxProfile{path: p})
			}
		}
	}

	existing := candidates[:0]
	for _, c := range candidates {
		if _, err := os.Stat(c.path); err == nil {
			existing = append(existing, c)
		}
	}
	sort.SliceStable(existing, func(i, j int) bool {
		return existing[i].isDefault && !existing[j].isDefault
	})
	return existing, nil
}

func pickFirefoxProfiles(candidates []firefoxProfile, selector string) []firefoxProfile {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return candidates
	}
	needle := strings.ToLower(selector)
	var matched []firefoxProfile
	for _, c := range candidates {
		base := strings.ToLower(filepath.Base(c.path))
		full := strings.ToLower(c.path)
		if strings.ToLower(c.name) == needle || base == needle || strings.Contains(full, needle) {
			matched = append(matched, c)
		}
	}
	return matched
}

var (
	ffRichTeamRe = regexp.MustCompile(`(?s)"name":"([^"]+)".*?"url":"(https://[^"\s]+slack\.com/)".*?"token":"(xoxc-[^"]+)"`)
	ffURLRe      = regexp.MustCompile(`"url":"(https://[^"\s]+slack\.com/)"`)
	ffTokenRe    = regexp.MustCompile(`"token":"(xoxc-[^"]+)"`)
)

// extractTeamsFromRawText recovers team triplets from partially-damaged Firefox
// storage blobs where strict JSON parsing failed.
func extractTeamsFromRawText(raw string) []Team {
	var teams []Team
	seen := map[string]bool{}
	for _, m := range ffRichTeamRe.FindAllStringSubmatch(raw, -1) {
		key := m[2] + "::" + m[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		teams = append(teams, Team{Name: m[1], URL: m[2], Token: m[3]})
	}
	if len(teams) > 0 {
		return teams
	}
	urls := ffURLRe.FindAllStringSubmatch(raw, -1)
	tokens := ffTokenRe.FindAllStringSubmatch(raw, -1)
	n := len(urls)
	if len(tokens) < n {
		n = len(tokens)
	}
	for i := 0; i < n; i++ {
		key := urls[i][1] + "::" + tokens[i][1]
		if seen[key] {
			continue
		}
		seen[key] = true
		teams = append(teams, Team{URL: urls[i][1], Token: tokens[i][1]})
	}
	return teams
}

func firefoxTeamsFromProfile(profilePath string) ([]Team, string, bool) {
	lsDir := filepath.Join(profilePath, "storage", "default", "https+++app.slack.com", "ls")
	dbPath := filepath.Join(lsDir, "data.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, "", false
	}
	copyPath, cleanup, err := copySqliteForRead(dbPath)
	if err != nil {
		return nil, "", false
	}
	defer cleanup()

	rows, err := queryReadonlySqlite(copyPath,
		"select key, value from data where key in ('localConfig_v2', 'localConfig_v3') order by key desc")
	if err != nil {
		return nil, "", false
	}
	for _, row := range rows {
		blob := rowBytes(row, "value")
		if cfg, err := parseLocalConfig(blob); err == nil {
			if teams := teamsFromLocalConfig(cfg); len(teams) > 0 {
				return teams, dbPath, true
			}
		}
		if teams := extractTeamsFromRawText(string(blob)); len(teams) > 0 {
			return teams, dbPath, true
		}
	}
	return nil, "", false
}

func firefoxCookieFromProfile(profilePath string) (string, bool) {
	dbPath := filepath.Join(profilePath, "cookies.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return "", false
	}
	copyPath, cleanup, err := copySqliteForRead(dbPath)
	if err != nil {
		return "", false
	}
	defer cleanup()

	rows, err := queryReadonlySqlite(copyPath,
		"select value from moz_cookies where host like '%slack.com%' and name='d' order by length(value) desc")
	if err != nil {
		return "", false
	}
	for _, row := range rows {
		if v := rowString(row, "value"); strings.HasPrefix(v, "xoxd-") {
			return decodeFirefoxCookie(v), true
		}
	}
	return "", false
}

func decodeFirefoxCookie(cookie string) string {
	current := cookie
	for i := 0; i < 3; i++ {
		next, err := url.PathUnescape(current)
		if err != nil || next == current {
			break
		}
		current = next
	}
	return current
}

// ExtractFromFirefox imports xoxc tokens and the xoxd cookie from a Firefox
// profile's local storage and cookie databases. An optional profile selector
// (name, directory basename, or path substring) narrows which profile is used.
func ExtractFromFirefox(profileSelector string) (*Extracted, error) {
	all, err := listFirefoxProfiles()
	if err != nil {
		return nil, err
	}
	candidates := pickFirefoxProfiles(all, profileSelector)
	if len(candidates) == 0 {
		return nil, agenterrors.New("no matching Firefox profile found; open Slack in Firefox and sign in, then retry", agenterrors.FixableByHuman)
	}

	for _, c := range candidates {
		teams, lsPath, ok := firefoxTeamsFromProfile(c.path)
		if !ok {
			continue
		}
		cookie, ok := firefoxCookieFromProfile(c.path)
		if !ok {
			continue
		}
		return &Extracted{
			CookieD: cookie,
			Teams:   teams,
			Source:  map[string]string{"profile_path": c.path, "localstorage_path": lsPath, "cookies_path": filepath.Join(c.path, "cookies.sqlite")},
		}, nil
	}
	return nil, agenterrors.New("could not find a Firefox profile with a complete Slack session (tokens + cookie)", agenterrors.FixableByHuman)
}
