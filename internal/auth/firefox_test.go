package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProfilesIni(t *testing.T) {
	base := "/home/u/.mozilla/firefox"
	ini := `
[Install ABC]
Default=default-release.dir

[Profile0]
Name=default
IsRelative=1
Path=old.default
Default=1

[Profile1]
Name=dev-edition
IsRelative=1
Path=default-release.dir

[Profile2]
Name=absolute
IsRelative=0
Path=/custom/profile
`
	profiles := parseProfilesIni(ini, base)
	if len(profiles) != 3 {
		t.Fatalf("expected 3 profiles, got %d: %+v", len(profiles), profiles)
	}

	byName := map[string]firefoxProfile{}
	for _, p := range profiles {
		byName[p.name] = p
	}

	if got := byName["default"]; got.path != filepath.Join(base, "old.default") || !got.isDefault {
		t.Errorf("default profile wrong: %+v", got)
	}
	// Marked default via [Install] Default=default-release.dir
	if got := byName["dev-edition"]; !got.isDefault {
		t.Errorf("dev-edition should be default via Install section: %+v", got)
	}
	if got := byName["absolute"]; got.path != "/custom/profile" {
		t.Errorf("absolute path not honored: %+v", got)
	}
}

func TestPickFirefoxProfiles(t *testing.T) {
	cands := []firefoxProfile{
		{name: "default", path: "/p/abc.default"},
		{name: "work", path: "/p/xyz.work"},
	}
	if got := pickFirefoxProfiles(cands, ""); len(got) != 2 {
		t.Errorf("empty selector should return all, got %d", len(got))
	}
	if got := pickFirefoxProfiles(cands, "work"); len(got) != 1 || got[0].name != "work" {
		t.Errorf("name selector failed: %+v", got)
	}
	if got := pickFirefoxProfiles(cands, "xyz"); len(got) != 1 || got[0].name != "work" {
		t.Errorf("basename selector failed: %+v", got)
	}
	if got := pickFirefoxProfiles(cands, "nope"); len(got) != 0 {
		t.Errorf("non-matching selector should return none, got %d", len(got))
	}
}

func TestExtractTeamsFromRawText(t *testing.T) {
	raw := `garbage {"name":"Acme","url":"https://acme.slack.com/","token":"xoxc-aaa"} more {"name":"Globex","url":"https://globex.slack.com/","token":"xoxc-bbb"}`
	teams := extractTeamsFromRawText(raw)
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams, got %d: %+v", len(teams), teams)
	}
	if teams[0].Name != "Acme" || teams[0].Token != "xoxc-aaa" {
		t.Errorf("first team wrong: %+v", teams[0])
	}
}

func TestDecodeFirefoxCookie(t *testing.T) {
	if got := decodeFirefoxCookie("xoxd-Ab%252FCd"); got != "xoxd-Ab/Cd" {
		t.Errorf("decodeFirefoxCookie double-encoded = %q", got)
	}
	if got := decodeFirefoxCookie("xoxd-plain"); got != "xoxd-plain" {
		t.Errorf("decodeFirefoxCookie plain = %q", got)
	}
}

func TestListFirefoxProfilesIn(t *testing.T) {
	base := t.TempDir()
	// darwin layout: profiles live under Profiles/, profiles.ini at the root.
	mk := func(rel string) string {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	defaultProfile := mk("Profiles/abc.default-release")
	extraProfile := mk("Profiles/xyz.dev-edition")
	mk("Profiles/not-a-profile.txt-dir") // picked up too: discovery is permissive

	ini := `[Install4F96D1932A9F858E]
Default=Profiles/abc.default-release

[Profile0]
Name=default-release
IsRelative=1
Path=Profiles/abc.default-release

[Profile1]
Name=missing
IsRelative=1
Path=Profiles/deleted-profile
`
	if err := os.WriteFile(filepath.Join(base, "profiles.ini"), []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}

	profiles := listFirefoxProfilesIn(base, true)

	if len(profiles) != 3 { // default + extra + permissive dir; deleted-profile filtered (does not exist)
		t.Fatalf("profiles = %+v", profiles)
	}
	if profiles[0].path != defaultProfile || !profiles[0].isDefault {
		t.Errorf("default should sort first: %+v", profiles[0])
	}
	for _, p := range profiles {
		if p.path == filepath.Join(base, "Profiles/deleted-profile") {
			t.Error("nonexistent profile should be filtered out")
		}
	}
	// The unlisted dir was discovered without duplicating the ini-listed one.
	found := 0
	for _, p := range profiles {
		if p.path == extraProfile || p.path == defaultProfile {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected ini + scanned dirs deduped, got %+v", profiles)
	}
}
