package cli

import (
	"slices"
	"testing"

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
