package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/output"
)

// useHermeticStore points newStore at a temp file + in-memory keychain for the
// duration of a test, so CLI auth tests never touch the real config or Keychain.
func useHermeticStore(t *testing.T) *credential.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := credential.NewWithStore(path, credential.NewMemoryKeychain())
	prev := newStore
	newStore = func() (*credential.Store, error) { return store, nil }
	t.Cleanup(func() { newStore = prev })
	return store
}

func runCLI(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	restore := output.SetWriters(&out, &errBuf)
	defer restore()

	root := newRootCmd("test")
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = execute(root)
	return out.String(), errBuf.String(), err
}

func TestAuthAddStandardAndWhoami(t *testing.T) {
	useHermeticStore(t)

	if _, _, err := runCLI(t, "", "auth", "add", "--workspace-url", "https://acme.slack.com", "--token", "xoxb-12345678901234"); err != nil {
		t.Fatalf("auth add: %v", err)
	}

	out, _, err := runCLI(t, "", "auth", "whoami")
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
	useHermeticStore(t)
	_, stderr, err := runCLI(t, "", "auth", "add", "--workspace-url", "https://acme.slack.com")
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
	store := useHermeticStore(t)
	curl := `curl 'https://acme.slack.com/api/conversations.history' -H 'Cookie: d=xoxd-cookieval; x=1' --data 'token=xoxc-tok-123'`

	out, _, err := runCLI(t, curl, "auth", "parse-curl")
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
	useHermeticStore(t)
	_, stderr, err := runCLI(t, "   \n", "auth", "parse-curl")
	if err == nil {
		t.Fatal("expected error on empty stdin")
	}
	if !strings.Contains(stderr, "fixable_by") {
		t.Errorf("expected structured error, got %s", stderr)
	}
}

func TestAuthRemoveAndSetDefault(t *testing.T) {
	store := useHermeticStore(t)
	if _, _, err := runCLI(t, "", "auth", "add", "--workspace-url", "https://acme.slack.com", "--token", "xoxb-aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCLI(t, "", "auth", "add", "--workspace-url", "https://globex.slack.com", "--token", "xoxb-gggggggggggg"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runCLI(t, "", "auth", "set-default", "https://globex.slack.com"); err != nil {
		t.Fatalf("set-default: %v", err)
	}
	def, _ := store.ResolveDefault()
	if def.URL != "https://globex.slack.com" {
		t.Errorf("default not updated: %q", def.URL)
	}

	if _, _, err := runCLI(t, "", "auth", "remove", "https://acme.slack.com"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	creds, _ := store.Load()
	if len(creds.Workspaces) != 1 {
		t.Errorf("expected 1 workspace after remove, got %d", len(creds.Workspaces))
	}
}
