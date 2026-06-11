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
	useHermeticStore(t)
	_, stderr, err := runCLI(t, "", "auth", "test")
	if err == nil {
		t.Fatal("expected error")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" || !strings.Contains(payload["hint"].(string), "auth import-desktop") {
		t.Errorf("payload = %v", payload)
	}
}

func TestMultipleWorkspacesNeedSelectorOrDefault(t *testing.T) {
	store := useHermeticStore(t)
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
	creds.DefaultWorkspaceURL = ""
	if err := store.Save(creds); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := runCLI(t, "", "auth", "test")
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
	useHermeticStore(t) // empty store: env must carry the auth
	server := mockslack.New()
	server.HandleBody("auth.test", map[string]any{"ok": true, "user": "envuser"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	t.Setenv("SLACK_TOKEN", "xoxc-env-token")
	t.Setenv("SLACK_COOKIE_D", "xoxd-env-cookie")
	t.Setenv("SLACK_WORKSPACE_URL", ts.URL) // browser path calls the workspace host directly

	out, _, err := runCLI(t, "", "auth", "test")
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

func TestDesktopAutoRefresh(t *testing.T) {
	store := useHermeticStore(t)
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

	prev := desktopExtract
	extractions := 0
	desktopExtract = func() (*auth.Extracted, error) {
		extractions++
		return &auth.Extracted{
			CookieD: "xoxd-new",
			Teams:   []auth.Team{{URL: ts.URL, Name: "acme", Token: "xoxc-fresh"}},
		}, nil
	}
	t.Cleanup(func() { desktopExtract = prev })

	out, stderr, err := runCLI(t, "", "auth", "test")
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

func TestEnvCredentialsDoNotAutoRefresh(t *testing.T) {
	useHermeticStore(t)
	server := mockslack.New()
	server.ExpectToken = "xoxc-good"
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	t.Setenv("SLACK_TOKEN", "xoxc-stale")
	t.Setenv("SLACK_COOKIE_D", "xoxd-x")
	t.Setenv("SLACK_WORKSPACE_URL", ts.URL)

	prev := desktopExtract
	called := false
	desktopExtract = func() (*auth.Extracted, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { desktopExtract = prev })

	_, stderr, err := runCLI(t, "", "auth", "test")
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
