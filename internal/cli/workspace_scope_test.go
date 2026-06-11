package cli

import (
	"net/http/httptest"
	"testing"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/mockslack"
)

// Pins the multi-workspace scoping contract: with a default set, every
// command runs against the default's credentials unless --workspace selects
// another, or a permalink target names one explicitly. There is no other
// route into a workspace, and an unconfigured workspace is an error — never
// a silent fallback to the default.
func TestWorkspaceScopingContract(t *testing.T) {
	env := newTestEnv(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// One mock server; which WORKSPACE a call used is visible from the
	// standard-auth Bearer token it carried.
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	for _, ws := range []struct{ url, token string }{
		{"https://acme.slack.com", "xoxb-acme"},
		{"https://globex.slack.com", "xoxb-globex"},
	} {
		if _, err := env.store.Upsert(credential.Workspace{
			URL:  ws.url,
			Auth: credential.Auth{Type: credential.AuthStandard, Token: ws.token},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.store.SetDefault("https://acme.slack.com"); err != nil {
		t.Fatal(err)
	}
	server.HandleBody("auth.test", map[string]any{"ok": true})
	server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1770165109.628379", "U1", "hi"),
	))
	server.HandleBody("conversations.replies", mockslack.History())

	run := func(args ...string) (string, string, error) {
		return env.run(t, "", append([]string{"--base-url", ts.URL}, args...)...)
	}
	lastToken := func() string {
		calls := server.Calls()
		return calls[len(calls)-1].Header.Get("Authorization")
	}

	// 1. No selector → the default workspace's credentials.
	if _, _, err := run("auth", "test"); err != nil {
		t.Fatal(err)
	}
	if lastToken() != "Bearer xoxb-acme" {
		t.Errorf("default scoping used %q", lastToken())
	}

	// 2. --workspace (substring selector) switches workspaces.
	if _, _, err := run("auth", "test", "--workspace", "globex"); err != nil {
		t.Fatal(err)
	}
	if lastToken() != "Bearer xoxb-globex" {
		t.Errorf("--workspace scoping used %q", lastToken())
	}

	// 3. A permalink names its workspace and overrides the default — the only
	// non-flag route into another workspace.
	if _, _, err := run("message", "get", "https://globex.slack.com/archives/C1A2B3C4D/p1770165109628379"); err != nil {
		t.Fatal(err)
	}
	if lastToken() != "Bearer xoxb-globex" {
		t.Errorf("permalink scoping used %q", lastToken())
	}

	// 3b. A channel URL (no message segment) pins its workspace the same way.
	if _, _, err := run("message", "list", "https://globex.slack.com/archives/C1A2B3C4D"); err != nil {
		t.Fatal(err)
	}
	if lastToken() != "Bearer xoxb-globex" {
		t.Errorf("channel-URL scoping used %q", lastToken())
	}

	// 4. A permalink to an UNCONFIGURED workspace errors with the configured
	// list — it must not fall back to the default workspace's credentials.
	callsBefore := len(server.Calls())
	_, stderr, err := run("message", "get", "https://unconfigured.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err == nil {
		t.Fatal("expected error for unconfigured workspace")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("stderr = %s", stderr)
	}
	if len(server.Calls()) != callsBefore {
		t.Error("unconfigured-workspace permalink leaked API calls")
	}
}
