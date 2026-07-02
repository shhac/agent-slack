package cli

import (
	"path/filepath"
	"testing"

	output "github.com/shhac/lib-agent-output"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/lib-agent-mcp/oauth"
)

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
