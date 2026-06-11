package credential

import (
	"path/filepath"
	"testing"
)

// The agent-* family stores config under ~/.config/<tool> on every platform
// (XDG_CONFIG_HOME-aware), never under macOS's Application Support.
func TestDefaultPathFollowsFamilyConvention(t *testing.T) {
	t.Setenv("AGENT_SLACK_CREDENTIALS", "")

	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	got, err := defaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/tmp/xdg-test", "agent-slack", "credentials.json") {
		t.Errorf("XDG path = %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	got, err = defaultPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/", ".config", "agent-slack", "credentials.json")
	if !filepath.IsAbs(got) || filepath.Base(got) != "credentials.json" ||
		filepath.Base(filepath.Dir(got)) != "agent-slack" ||
		filepath.Base(filepath.Dir(filepath.Dir(got))) != ".config" {
		t.Errorf("fallback path = %q, want …%s", got, want[1:])
	}

	t.Setenv("AGENT_SLACK_CREDENTIALS", "/custom/creds.json")
	got, err = defaultPath()
	if err != nil || got != "/custom/creds.json" {
		t.Errorf("env override = %q, %v", got, err)
	}
}
