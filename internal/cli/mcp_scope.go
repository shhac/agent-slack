package cli

import (
	"path/filepath"

	agentmcp "github.com/shhac/lib-agent-mcp"
	"github.com/shhac/lib-agent-mcp/oauth"
	output "github.com/shhac/lib-agent-output"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/slack"
)

// mcpFileRootScope narrows a named MCP principal's fs access to the identity
// subtree (<team_id>/<user_id>) of the workspace alias their pairing is
// bound to — the same namespace their own downloads land in. Anything that
// can't be resolved (no workspace binding, unknown alias, identity not yet
// learned from auth.test) hides the root: fail closed, never the whole cache.
func mcpFileRootScope(newStore func() (*credential.Store, error)) agentmcp.FileRootScope {
	return func(p oauth.Verified, root output.FileRoot) (output.FileRoot, bool) {
		alias := p.Binding["workspace"]
		if alias == "" {
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
