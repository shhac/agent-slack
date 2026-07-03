package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shhac/lib-agent-mcp/oauth"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/mockslack"
)

// enrollFixture wires the enroll callback to a mockslack server and a scratch
// store: the standard-token path reaches the mock via --base-url; the browser
// path reaches it by using the mock's URL as the workspace URL.
type enrollFixture struct {
	env    *testEnv
	server *mockslack.Server
	url    string
	enroll oauth.EnrollFunc
}

func newEnrollFixture(t *testing.T) *enrollFixture {
	t.Helper()
	env := newTestEnv(t)
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	globals := &GlobalFlags{
		version:  "test",
		newStore: func() (*credential.Store, error) { return env.store, nil },
		stderr:   &bytes.Buffer{},
	}
	globals.BaseURL = ts.URL
	return &enrollFixture{env: env, server: server, url: ts.URL, enroll: mcpEnroll(globals)}
}

func (f *enrollFixture) authTestReturns(body map[string]any) { f.server.HandleBody("auth.test", body) }

func (f *enrollFixture) run(t *testing.T, principal string, values map[string]string) (oauth.EnrollResult, error) {
	t.Helper()
	return f.enroll(context.Background(), oauth.EnrollRequest{Principal: principal, Mode: "slack", Values: values})
}

func TestMCPEnrollStandardToken(t *testing.T) {
	f := newEnrollFixture(t)
	f.authTestReturns(map[string]any{
		"ok": true, "url": "https://acme.slack.com/", "team": "Acme",
		"team_id": "T0ACME", "user_id": "U0ALICE",
	})

	res, err := f.run(t, "alice", map[string]string{"token": "xoxp-alice-token"})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if res.Binding["workspace"] != "alice" {
		t.Errorf("binding = %v, want workspace=alice", res.Binding)
	}

	ws, err := f.env.store.Resolve("alice")
	if err != nil {
		t.Fatal(err)
	}
	// URL derived from auth.test (trailing slash trimmed), identity persisted,
	// alias = principal.
	if ws.URL != "https://acme.slack.com" || ws.TeamID != "T0ACME" || ws.UserID != "U0ALICE" ||
		ws.Name != "Acme" || ws.Auth.Type != credential.AuthStandard || ws.Auth.Token != "xoxp-alice-token" {
		t.Errorf("stored workspace = %+v", ws)
	}
}

func TestMCPEnrollBrowserToken(t *testing.T) {
	f := newEnrollFixture(t)
	f.authTestReturns(map[string]any{
		"ok": true, "team": "Acme", "team_id": "T0ACME", "user_id": "U0ALICE",
	})

	// The browser transport posts to the workspace URL, so point it at the mock.
	res, err := f.run(t, "alice", map[string]string{
		"token": "xoxc-alice", "xoxd": "xoxd-cookie", "workspace_url": f.url + "/",
	})
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if res.Binding["workspace"] != "alice" {
		t.Errorf("binding = %v", res.Binding)
	}
	ws, _ := f.env.store.Resolve("alice")
	if ws.Auth.Type != credential.AuthBrowser || ws.Auth.XOXC != "xoxc-alice" || ws.Auth.XOXD != "xoxd-cookie" {
		t.Errorf("stored auth = %+v", ws.Auth)
	}
	if got := f.server.CallsFor("auth.test")[0].Params.Get("token"); got != "xoxc-alice" {
		t.Errorf("auth.test token = %q", got)
	}
}

func TestMCPEnrollXOXCNeedsCookieAndURL(t *testing.T) {
	f := newEnrollFixture(t)

	if _, err := f.run(t, "alice", map[string]string{"token": "xoxc-alice"}); err == nil ||
		!strings.Contains(err.Error(), "session cookie") {
		t.Errorf("missing cookie: err = %v, want session-cookie guidance", err)
	}
	if _, err := f.run(t, "alice", map[string]string{"token": "xoxc-alice", "xoxd": "xoxd-c"}); err == nil ||
		!strings.Contains(err.Error(), "workspace URL") {
		t.Errorf("missing URL: err = %v, want workspace-URL guidance", err)
	}
	// Nothing was stored and Slack was never called.
	if _, err := f.env.store.Resolve("alice"); err == nil {
		t.Error("failed validation must not store credentials")
	}
	if calls := f.server.CallsFor("auth.test"); len(calls) != 0 {
		t.Errorf("auth.test called %d times before local validation passed", len(calls))
	}
}

func TestMCPEnrollRejectedBySlack(t *testing.T) {
	f := newEnrollFixture(t)
	f.authTestReturns(map[string]any{"ok": false, "error": "invalid_auth"})

	_, err := f.run(t, "alice", map[string]string{"token": "xoxp-bad"})
	if err == nil || !strings.Contains(err.Error(), "invalid_auth") {
		t.Errorf("err = %v, want the Slack error surfaced", err)
	}
	if _, rerr := f.env.store.Resolve("alice"); rerr == nil {
		t.Error("rejected credentials must not be stored")
	}
}

// The convergence rule: a slot that knows its Slack identity only accepts
// credentials proving the same identity.
func TestMCPEnrollIdentityConvergence(t *testing.T) {
	f := newEnrollFixture(t)
	if _, err := f.env.store.Upsert(credential.Workspace{
		Alias: "alice", URL: "https://acme.slack.com", TeamID: "T0ACME", UserID: "U0ALICE",
		Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxp-old"},
	}); err != nil {
		t.Fatal(err)
	}

	// The mock queue is FIFO with a sticky tail: first call sees the foreign
	// identity, every later call the matching one.
	f.server.Handle("auth.test",
		mockslack.Response{Body: map[string]any{
			"ok": true, "url": "https://other.slack.com", "team_id": "T0OTHER", "user_id": "U0BOB"}},
		mockslack.Response{Body: map[string]any{
			"ok": true, "url": "https://acme.slack.com", "team_id": "T0ACME", "user_id": "U0ALICE"}},
	)

	// Different identity → refused, slot untouched.
	if _, err := f.run(t, "alice", map[string]string{"token": "xoxp-bob"}); err == nil ||
		!strings.Contains(err.Error(), "different Slack account") {
		t.Errorf("err = %v, want a different-account refusal", err)
	}
	if ws, _ := f.env.store.Resolve("alice"); ws.Auth.Token != "xoxp-old" {
		t.Errorf("slot overwritten by refused enrollment: %+v", ws.Auth)
	}

	// Same identity → idempotent re-enrollment updates the secret in place.
	res, err := f.run(t, "alice", map[string]string{"token": "xoxp-new"})
	if err != nil {
		t.Fatalf("re-enroll same identity: %v", err)
	}
	if res.Binding["workspace"] != "alice" {
		t.Errorf("binding = %v", res.Binding)
	}
	if ws, _ := f.env.store.Resolve("alice"); ws.Auth.Token != "xoxp-new" {
		t.Errorf("re-enrollment did not update the token: %+v", ws.Auth)
	}
}

// A principal whose name happens to match another workspace's URL/name must
// not converge against it — the check is strictly by alias.
func TestMCPEnrollConvergenceIsAliasStrict(t *testing.T) {
	f := newEnrollFixture(t)
	if _, err := f.env.store.Upsert(credential.Workspace{
		Alias: "ops", URL: "https://alice.slack.com", Name: "alice", TeamID: "T0OPS", UserID: "U0OPS",
		Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxp-ops"},
	}); err != nil {
		t.Fatal(err)
	}
	f.authTestReturns(map[string]any{
		"ok": true, "url": "https://acme.slack.com", "team_id": "T0ACME", "user_id": "U0ALICE",
	})
	if _, err := f.run(t, "alice", map[string]string{"token": "xoxp-alice"}); err != nil {
		t.Fatalf("enrollment blocked by an unrelated workspace whose name matches the principal: %v", err)
	}
}

func TestMCPEnrollmentDescriptorShape(t *testing.T) {
	d := mcpEnrollmentDescriptor()
	if len(d.Modes) != 1 {
		t.Fatalf("modes = %d, want the single smart mode", len(d.Modes))
	}
	var keys []string
	for _, f := range d.Modes[0].Fields {
		keys = append(keys, f.Key)
		if f.Key == "token" && (f.Optional || !f.Secret) {
			t.Error("token must be a required secret")
		}
		if f.Key == "xoxd" && (!f.Optional || !f.Secret) {
			t.Error("xoxd must be an optional secret")
		}
		if f.Key == "workspace_url" && (!f.Optional || f.Secret) {
			t.Error("workspace_url must be optional and non-secret")
		}
	}
	if strings.Join(keys, ",") != "token,xoxd,workspace_url" {
		t.Errorf("fields = %v", keys)
	}
}
