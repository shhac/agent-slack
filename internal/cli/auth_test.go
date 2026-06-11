package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/auth"
	"github.com/shhac/agent-slack/internal/credential"
)

// testEnv carries the hermetic deps a test root is built with — a temp-file
// store with an in-memory keychain, and a desktop extractor that fails unless
// a test installs one. Constructor injection: no globals, tests can run in
// parallel.
type testEnv struct {
	store          *credential.Store
	desktopExtract func() (*auth.Extracted, error)
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	return &testEnv{
		store: credential.NewWithStore(path, credential.NewMemoryKeychain()),
		desktopExtract: func() (*auth.Extracted, error) {
			return nil, errors.New("desktop extraction unavailable in tests")
		},
	}
}

func (e *testEnv) run(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmdWithDeps(rootDeps{
		version:        "test",
		newStore:       func() (*credential.Store, error) { return e.store, nil },
		desktopExtract: func() (*auth.Extracted, error) { return e.desktopExtract() },
	})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = execute(root)
	return out.String(), errBuf.String(), err
}

func TestAuthAddStandardAndWhoami(t *testing.T) {
	env := newTestEnv(t)

	if _, _, err := env.run(t, "", "auth", "add", "--workspace-url", "https://acme.slack.com", "--token", "xoxb-12345678901234"); err != nil {
		t.Fatalf("auth add: %v", err)
	}

	out, _, err := env.run(t, "", "auth", "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	var who map[string]any
	if err := json.Unmarshal([]byte(out), &who); err != nil {
		t.Fatalf("whoami output not JSON: %v\n%s", err, out)
	}
	if who["default_workspace_url"] != "https://acme.slack.com" {
		t.Errorf("default workspace = %v", who["default_workspace_url"])
	}
	if strings.Contains(out, "xoxb-12345678901234") {
		t.Errorf("whoami leaked the raw token:\n%s", out)
	}
	workspaces := who["workspaces"].([]any)
	if len(workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(workspaces))
	}
}

func TestAuthAddRequiresCredentials(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, err := env.run(t, "", "auth", "add", "--workspace-url", "https://acme.slack.com")
	if err == nil {
		t.Fatal("expected error when no token/xoxc given")
	}
	var payload map[string]any
	if jerr := json.Unmarshal([]byte(stderr), &payload); jerr != nil {
		t.Fatalf("error not JSON: %v\n%s", jerr, stderr)
	}
	if payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v, want agent", payload["fixable_by"])
	}
}

func TestAuthParseCurl(t *testing.T) {
	env := newTestEnv(t)
	store := env.store
	curl := `curl 'https://acme.slack.com/api/conversations.history' -H 'Cookie: d=xoxd-cookieval; x=1' --data 'token=xoxc-tok-123'`

	out, _, err := env.run(t, curl, "auth", "parse-curl")
	if err != nil {
		t.Fatalf("parse-curl: %v", err)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatalf("summary not JSON: %v\n%s", err, out)
	}
	if summary["imported"].(float64) != 1 {
		t.Errorf("imported = %v, want 1", summary["imported"])
	}

	ws, err := store.Resolve("acme")
	if err != nil {
		t.Fatalf("resolve after import: %v", err)
	}
	if ws.Auth.Type != credential.AuthBrowser || ws.Auth.XOXC != "xoxc-tok-123" || ws.Auth.XOXD != "xoxd-cookieval" {
		t.Errorf("stored auth wrong: %+v", ws.Auth)
	}
}

func TestAuthParseCurlEmptyStdin(t *testing.T) {
	env := newTestEnv(t)
	_, stderr, err := env.run(t, "   \n", "auth", "parse-curl")
	if err == nil {
		t.Fatal("expected error on empty stdin")
	}
	if !strings.Contains(stderr, "fixable_by") {
		t.Errorf("expected structured error, got %s", stderr)
	}
}

func TestAuthRemoveAndSetDefault(t *testing.T) {
	env := newTestEnv(t)
	store := env.store
	if _, _, err := env.run(t, "", "auth", "add", "--workspace-url", "https://acme.slack.com", "--token", "xoxb-aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.run(t, "", "auth", "add", "--workspace-url", "https://globex.slack.com", "--token", "xoxb-gggggggggggg"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := env.run(t, "", "auth", "set-default", "https://globex.slack.com"); err != nil {
		t.Fatalf("set-default: %v", err)
	}
	def, _ := store.ResolveDefault()
	if def.URL != "https://globex.slack.com" {
		t.Errorf("default not updated: %q", def.URL)
	}

	if _, _, err := env.run(t, "", "auth", "remove", "https://acme.slack.com"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	creds, _ := store.Load()
	if len(creds.Workspaces) != 1 {
		t.Errorf("expected 1 workspace after remove, got %d", len(creds.Workspaces))
	}
}

func TestAuthTest(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul", "team": "Acme", "user_id": "U12345678"})

	out, _, err := f.run(t, "auth", "test")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["user"] != "paul" || payload["auth_type"] != "standard" {
		t.Errorf("payload = %v", payload)
	}
	if got := f.server.CallsFor("auth.test")[0].Header.Get("Authorization"); got != "Bearer xoxb-test-token" {
		t.Errorf("authorization = %q", got)
	}
}
