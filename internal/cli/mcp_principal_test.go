// Tests for the per-principal MCP wiring (mcp_principal.go).
package cli

import (
	"path/filepath"
	"slices"
	"testing"

	output "github.com/shhac/lib-agent-output"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/lib-agent-mcp/oauth"
)

// mcpIdentityBinding is agent-slack's half of the multi-user MCP contract:
// a named principal's tool calls are pinned to their workspace alias and run
// fail-closed, so a binding-plumbing bug errors instead of acting as the
// operator's default identity.
func TestMCPIdentityBindingPinsWorkspaceAndFailsClosed(t *testing.T) {
	argv, env := mcpIdentityBinding(oauth.Verified{
		PrincipalGrant: oauth.PrincipalGrant{
			Name:    "alice",
			Binding: map[string]string{"workspace": "alice-acme"},
		},
	})
	if !slices.Equal(argv, []string{"--workspace", "alice-acme"}) {
		t.Errorf("argv = %v", argv)
	}
	if !slices.Contains(env, "AGENT_SLACK_REQUIRE_IDENTITY=1") {
		t.Errorf("env missing fail-closed gate: %v", env)
	}
}

// A principal paired without a workspace binding gets no selector but keeps
// the fail-closed gate — their calls refuse rather than falling back to the
// operator's default workspace.
func TestMCPIdentityBindingWithoutWorkspaceStillFailsClosed(t *testing.T) {
	argv, env := mcpIdentityBinding(oauth.Verified{
		PrincipalGrant: oauth.PrincipalGrant{Name: "bob"},
	})
	if len(argv) != 0 {
		t.Errorf("argv should be empty without a workspace binding: %v", argv)
	}
	if !slices.Contains(env, "AGENT_SLACK_REQUIRE_IDENTITY=1") {
		t.Errorf("env missing fail-closed gate: %v", env)
	}
}

// mcpFileRootScope narrows a named principal's fs access to the identity
// subtree (<team_id>/<user_id>) of the workspace alias their pairing is bound
// to — and hides the root entirely when that can't be resolved.
func TestMCPFileRootScopeNarrowsToIdentitySubtree(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.store.Upsert(credential.Workspace{
		Alias: "alice", URL: "https://acme.slack.com",
		TeamID: "T1", UserID: "U1",
		Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-a"},
	}); err != nil {
		t.Fatal(err)
	}
	scope := mcpFileRootScope(func() (*credential.Store, error) { return env.store, nil })
	root := output.FileRoot{Name: "cache", Path: "/tmp/cache-root"}

	scoped, ok := scope(oauth.Verified{PrincipalGrant: oauth.PrincipalGrant{
		Name: "alice", Binding: map[string]string{"workspace": "alice"}}}, root)
	if !ok {
		t.Fatal("resolvable principal lost its root")
	}
	if scoped.Name != "cache" || scoped.Path != filepath.Join("/tmp/cache-root", "T1", "U1") {
		t.Errorf("scoped = %+v", scoped)
	}
}

func TestMCPFileRootScopeFailsClosed(t *testing.T) {
	env := newTestEnv(t)
	// A workspace whose identity is not yet resolved (no team/user ids).
	if _, err := env.store.Upsert(credential.Workspace{
		Alias: "fresh", URL: "https://fresh.slack.com",
		Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-f"},
	}); err != nil {
		t.Fatal(err)
	}
	scope := mcpFileRootScope(func() (*credential.Store, error) { return env.store, nil })
	root := output.FileRoot{Name: "cache", Path: "/tmp/cache-root"}

	cases := map[string]oauth.PrincipalGrant{
		"no workspace binding": {Name: "bob"},
		"unknown alias":        {Name: "bob", Binding: map[string]string{"workspace": "nope"}},
		"unresolved identity":  {Name: "bob", Binding: map[string]string{"workspace": "fresh"}},
	}
	for name, grant := range cases {
		if _, ok := scope(oauth.Verified{PrincipalGrant: grant}, root); ok {
			t.Errorf("%s: scope should hide the root", name)
		}
	}
}
