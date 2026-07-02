package cli

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/auth"
	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestNoCredentialsHint(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, err := env.run(t, "", "auth", "test")
	if err == nil {
		t.Fatal("expected error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" || !strings.Contains(payload["hint"].(string), "auth import-desktop") {
		t.Errorf("payload = %v", payload)
	}
}

func TestMultipleWorkspacesNeedSelectorOrDefault(t *testing.T) {
	env := newTestEnv(t)
	store := env.store
	for _, url := range []string{"https://one.slack.com", "https://two.slack.com"} {
		if _, err := store.Upsert(credential.Workspace{
			URL:  url,
			Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-x"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Upsert of the first workspace may set it as default; clear it.
	creds, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	creds.DefaultWorkspace = ""
	if err := store.Save(creds); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := env.run(t, "", "auth", "test")
	if err == nil {
		t.Fatal("expected error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" || !strings.Contains(payload["hint"].(string), "set-default") {
		t.Errorf("payload = %v", payload)
	}
}

func TestWorkspaceSelectorNotFoundEnumerates(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "auth", "test", "--workspace", "globex")
	if err == nil {
		t.Fatal("expected error")
	}
	payload := errPayload(t, stderr)
	if !strings.Contains(payload["error"].(string), "https://acme.slack.com") {
		t.Errorf("error should enumerate configured workspaces: %v", payload)
	}
}

func TestEnvCredentialsUsed(t *testing.T) {
	env := newTestEnv(t) // empty store: env vars must carry the auth
	server := mockslack.New()
	server.HandleBody("auth.test", map[string]any{"ok": true, "user": "envuser"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	t.Setenv("SLACK_TOKEN", "xoxc-env-token")
	t.Setenv("SLACK_COOKIE_D", "xoxd-env-cookie")
	t.Setenv("SLACK_WORKSPACE_URL", ts.URL) // browser path calls the workspace host directly

	out, _, err := env.run(t, "", "auth", "test")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["user"] != "envuser" || payload["auth_type"] != "browser" {
		t.Errorf("payload = %v", payload)
	}
	call := server.CallsFor("auth.test")[0]
	if call.Params.Get("token") != "xoxc-env-token" {
		t.Errorf("token = %q", call.Params.Get("token"))
	}
}

func TestBootstrapResolvesAndPersistsIdentity(t *testing.T) {
	env := newTestEnv(t)
	server := mockslack.New()
	// auth.test now carries the identity the bootstrap learns and persists.
	server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul", "team_id": "T0BOOT", "user_id": "U0BOOT"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Stored credential with no identity yet — the first command must resolve it.
	if _, err := env.store.Upsert(credential.Workspace{
		URL:  "https://acme.slack.com",
		Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-x"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := env.run(t, "", "--base-url", ts.URL, "auth", "test"); err != nil {
		t.Fatalf("err = %v, stderr = %s", err, stderr)
	}

	ws, err := env.store.Resolve("https://acme.slack.com")
	if err != nil {
		t.Fatal(err)
	}
	if ws.TeamID != "T0BOOT" || ws.UserID != "U0BOOT" {
		t.Errorf("identity not persisted from bootstrap auth.test: %+v", ws)
	}
}

func TestBootstrapFailureLeavesIdentityUnresolved(t *testing.T) {
	env := newTestEnv(t)
	server := mockslack.New()
	server.HandleBody("auth.test", map[string]any{"ok": false, "error": "invalid_auth"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, err := env.store.Upsert(credential.Workspace{
		URL:  "https://acme.slack.com",
		Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-x"},
	}); err != nil {
		t.Fatal(err)
	}

	// The command itself fails (invalid auth); the invariant under test is that a
	// failed bootstrap must NOT persist a guessed identity — caching stays inert.
	_, _, _ = env.run(t, "", "--base-url", ts.URL, "auth", "test")

	ws, err := env.store.Resolve("https://acme.slack.com")
	if err != nil {
		t.Fatal(err)
	}
	if ws.TeamID != "" || ws.UserID != "" {
		t.Errorf("identity must stay unresolved after a failed bootstrap: %+v", ws)
	}
}

func TestBootstrapPartialIdentityNotPersisted(t *testing.T) {
	env := newTestEnv(t)
	server := mockslack.New()
	// auth.test succeeds but omits user_id — half an identity is not an identity.
	server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul", "team_id": "T0BOOT"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, err := env.store.Upsert(credential.Workspace{
		URL:  "https://acme.slack.com",
		Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-x"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := env.run(t, "", "--base-url", ts.URL, "auth", "test"); err != nil {
		t.Fatalf("err = %v, stderr = %s", err, stderr)
	}

	ws, err := env.store.Resolve("https://acme.slack.com")
	if err != nil {
		t.Fatal(err)
	}
	if ws.TeamID != "" || ws.UserID != "" {
		t.Errorf("a partial (team-only) identity must not be persisted: %+v", ws)
	}
}

func TestDesktopAutoRefresh(t *testing.T) {
	env := newTestEnv(t)
	store := env.store
	server := mockslack.New()
	server.ExpectToken = "xoxc-fresh"
	server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	// Browser workspace whose URL is the mock server; its stored token is stale.
	if _, err := store.Upsert(credential.Workspace{
		URL:  ts.URL,
		Name: "acme",
		Auth: credential.Auth{Type: credential.AuthBrowser, XOXC: "xoxc-stale", XOXD: "xoxd-c"},
	}); err != nil {
		t.Fatal(err)
	}

	extractions := 0
	env.desktopExtract = func() (*auth.Extracted, error) {
		extractions++
		return &auth.Extracted{
			CookieD: "xoxd-new",
			Teams:   []auth.Team{{URL: ts.URL, Name: "acme", Token: "xoxc-fresh"}},
		}, nil
	}

	out, stderr, err := env.run(t, "", "auth", "test")
	if err != nil {
		t.Fatalf("err = %v, stderr = %s", err, stderr)
	}
	if parseJSON(t, out)["user"] != "paul" {
		t.Errorf("out = %s", out)
	}
	if extractions != 1 {
		t.Errorf("extractions = %d", extractions)
	}
	if !strings.Contains(stderr, "refreshed") {
		t.Errorf("stderr should note the refresh: %q", stderr)
	}

	// The refreshed token persisted for the next run.
	ws, err := store.Resolve(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Auth.XOXC != "xoxc-fresh" || ws.Auth.XOXD != "xoxd-new" {
		t.Errorf("persisted auth = %+v", ws.Auth)
	}
}

func TestEnvBrowserCredentialsIncompleteFallThrough(t *testing.T) {
	env := newTestEnv(t) // empty store
	t.Setenv("SLACK_TOKEN", "xoxc-env")
	t.Setenv("SLACK_WORKSPACE_URL", "https://acme.slack.com")
	// SLACK_COOKIE_D is deliberately unset: an incomplete browser-auth env must
	// NOT serve the request (it would be missing the 'd' cookie) — it falls
	// through to the empty store, which then reports no credentials.
	_, stderr, err := env.run(t, "", "auth", "test")
	if err == nil {
		t.Fatal("expected an error: incomplete env browser credentials must not serve")
	}
	if errPayload(t, stderr)["fixable_by"] != "human" {
		t.Errorf("expected a human-fixable no-credentials error, got %s", stderr)
	}
}

func TestEnvCredentialsDoNotAutoRefresh(t *testing.T) {
	env := newTestEnv(t)
	server := mockslack.New()
	server.ExpectToken = "xoxc-good"
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	t.Setenv("SLACK_TOKEN", "xoxc-stale")
	t.Setenv("SLACK_COOKIE_D", "xoxd-x")
	t.Setenv("SLACK_WORKSPACE_URL", ts.URL)

	called := false
	env.desktopExtract = func() (*auth.Extracted, error) {
		called = true
		return nil, nil
	}

	_, stderr, err := env.run(t, "", "auth", "test")
	if err == nil {
		t.Fatal("expected auth error")
	}
	if called {
		t.Error("env-sourced credentials must not trigger desktop extraction")
	}
	if errPayload(t, stderr)["fixable_by"] != "human" {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestUnknownSubcommandIsStructuredError(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "message", "explode")
	if err == nil {
		t.Fatal("expected error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" {
		t.Errorf("payload = %v (want structured unknown-command error)", payload)
	}
}
