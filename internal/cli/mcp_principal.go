// Per-principal MCP wiring: how an authenticated named principal's pairing
// binding (mcp pair add <name> --bind workspace=<alias>) shapes their tool
// calls — the credential selector + fail-closed gate on every subprocess, and
// the fs view narrowed to their own cache subtree.
package cli

import (
	"path/filepath"

	agentmcp "github.com/shhac/lib-agent-mcp"
	"github.com/shhac/lib-agent-mcp/oauth"
	output "github.com/shhac/lib-agent-output"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/slack"
)

// bindingKeyWorkspace is the pairing-binding key naming the workspace alias a
// principal acts as — the vocabulary contract with
// `mcp pair add <name> --bind workspace=<alias>`.
const bindingKeyWorkspace = "workspace"

// workspaceAlias extracts the principal's workspace alias, reporting false
// when the pairing carried none — every consumer fails closed on that.
func workspaceAlias(p oauth.Verified) (string, bool) {
	alias := p.Binding[bindingKeyWorkspace]
	return alias, alias != ""
}

// mcpIdentityBinding translates an authenticated MCP principal into the
// subprocess shape their tool calls run with: `--workspace <alias>` pinning
// the credential set their pairing was bound to, plus the fail-closed gate so
// a call that arrives without a selector — a binding-plumbing bug — errors
// instead of falling back to the operator's default identity. The MCP layer
// only invokes this for named principals; the anonymous operator (stdio,
// legacy shared pairing code) stays unbound.
func mcpIdentityBinding(p oauth.Verified) (argv, env []string) {
	env = []string{"AGENT_SLACK_REQUIRE_IDENTITY=1"}
	alias, ok := workspaceAlias(p)
	if !ok {
		return nil, env
	}
	return []string{"--workspace", alias}, env
}

// mcpFileRootScope narrows a named MCP principal's fs access to the identity
// subtree (<team_id>/<user_id>) of the workspace alias their pairing is
// bound to — the same namespace their own downloads land in. Anything that
// can't be resolved (no workspace binding, unknown alias, identity not yet
// learned from auth.test) hides the root: fail closed, never the whole cache.
func mcpFileRootScope(newStore func() (*credential.Store, error)) agentmcp.FileRootScope {
	return func(p oauth.Verified, root output.FileRoot) (output.FileRoot, bool) {
		alias, ok := workspaceAlias(p)
		if !ok {
			return output.FileRoot{}, false
		}
		store, err := newStore()
		if err != nil {
			return output.FileRoot{}, false
		}
		ws, err := store.Resolve(alias)
		if err != nil {
			return output.FileRoot{}, false
		}
		key := slack.IdentityCacheKey(ws.TeamID, ws.UserID)
		if key == "" {
			return output.FileRoot{}, false
		}
		return output.FileRoot{
			Name: root.Name,
			Path: filepath.Join(root.Path, filepath.FromSlash(key)),
		}, true
	}
}
