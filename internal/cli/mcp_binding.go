package cli

import "github.com/shhac/lib-agent-mcp/oauth"

// mcpIdentityBinding translates an authenticated MCP principal into the
// subprocess shape their tool calls run with: `--workspace <alias>` pinning
// the credential set their pairing was bound to (`mcp pair add <name> --bind
// workspace=<alias>`), plus the fail-closed gate so a call that arrives
// without a selector — a binding-plumbing bug — errors instead of falling
// back to the operator's default identity. The MCP layer only invokes this
// for named principals; the anonymous operator (stdio, legacy shared pairing
// code) stays unbound.
func mcpIdentityBinding(p oauth.Verified) (argv, env []string) {
	env = []string{"AGENT_SLACK_REQUIRE_IDENTITY=1"}
	alias := p.Binding["workspace"]
	if alias == "" {
		return nil, env
	}
	return []string{"--workspace", alias}, env
}
