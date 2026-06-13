package auth

import (
	"strings"
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

func TestSupportedBrowsers(t *testing.T) {
	got := map[string]BrowserInfo{}
	for _, b := range SupportedBrowsers() {
		got[b.Name] = b
	}
	for _, name := range []string{"firefox", "zen", "chrome", "brave", "opera", "safari"} {
		if _, ok := got[name]; !ok {
			t.Errorf("registry missing browser %q", name)
		}
	}
	// Only the Firefox-family (Gecko) sources take a --profile selector.
	if !got["firefox"].SupportsProfile || !got["zen"].SupportsProfile {
		t.Error("firefox and zen should support --profile")
	}
	if got["chrome"].SupportsProfile || got["brave"].SupportsProfile || got["opera"].SupportsProfile || got["safari"].SupportsProfile {
		t.Error("chrome/brave/opera/safari should not support --profile")
	}
}

func TestImportBrowserUnknown(t *testing.T) {
	_, err := ImportBrowser("netscape", "")
	if err == nil {
		t.Fatal("expected an error for an unknown browser")
	}
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T", err)
	}
	if apiErr.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("fixable_by = %q, want agent", apiErr.FixableBy)
	}
	// The hint enumerates the supported names so a caller can correct the input.
	for _, name := range []string{"firefox", "zen", "chrome", "brave", "opera", "safari"} {
		if !strings.Contains(apiErr.Hint, name) {
			t.Errorf("hint %q missing supported browser %q", apiErr.Hint, name)
		}
	}
}

// TestExtractFromGeckoThreadsBaseDirAndName proves the Gecko seam: extraction
// runs against the supplied base dir (not a hardcoded Firefox path) and names
// the supplied browser in its error.
func TestExtractFromGeckoThreadsBaseDirAndName(t *testing.T) {
	empty := t.TempDir()
	_, err := extractFromGecko("Zen", func() (string, error) { return empty, nil }, "")
	if err == nil {
		t.Fatal("expected an error for a base dir with no profiles")
	}
	if !strings.Contains(err.Error(), "Zen") {
		t.Errorf("error %q should name the browser (Zen)", err.Error())
	}
}

func TestDisplayName(t *testing.T) {
	for in, want := range map[string]string{"firefox": "Firefox", "zen": "Zen", "": ""} {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}
