package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/auth"
	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerAuth(parent *cobra.Command, globals *GlobalFlags) {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage Slack credentials (tokens are stored in the macOS Keychain where available)",
	}
	parent.AddCommand(authCmd)
	handleUnknownSubcommand(authCmd)

	registerAuthList(authCmd, globals)
	registerAuthTest(authCmd, globals)
	registerAuthImport(authCmd, globals, "import-desktop",
		"Import xoxc tokens + the d cookie from Slack Desktop (no need to quit Slack)",
		auth.ExtractFromSlackDesktop)
	registerAuthImportBrowser(authCmd, globals)
	registerAuthParseCurl(authCmd, globals)
	registerAuthAdd(authCmd, globals)
	registerAuthSetDefault(authCmd, globals)
	registerAuthRemove(authCmd, globals)
}

func registerAuthTest(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Verify credentials against Slack's auth.test",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			resp, err := cc.Client.API(cmd.Context(), "auth.test", nil)
			if err != nil {
				return err
			}
			payload := map[string]any{
				"ok":            true,
				"workspace_url": cc.WorkspaceURL,
				"auth_type":     string(cc.AuthType),
			}
			for _, key := range []string{"url", "team", "user", "team_id", "user_id", "bot_id", "enterprise_id"} {
				if v, ok := resp[key].(string); ok && v != "" {
					payload[key] = v
				}
			}
			return printSingle(globals, payload)
		},
	}
	parent.AddCommand(cmd)
}

func registerAuthList(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "whoami"},
		Short:   "List configured workspaces and where each secret is stored (no secret material)",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			creds, err := store.Load()
			if err != nil {
				return err
			}
			statuses, err := store.SecretStatuses()
			if err != nil {
				return err
			}
			workspaces := make([]map[string]any, 0, len(creds.Workspaces))
			for _, w := range creds.Workspaces {
				entry := map[string]any{
					"alias":          w.Alias,
					"workspace_url":  w.URL,
					"workspace_name": w.Name,
					"auth_type":      string(w.Auth.Type),
					"secrets":        statuses[w.Alias],
				}
				if missing := credential.MissingSecrets(w); len(missing) > 0 {
					entry["hint"] = "secret missing from the Keychain; re-run 'agent-slack auth import-desktop' or 'agent-slack auth add --alias " + w.Alias + " --workspace-url " + w.URL + " --form'"
				}
				workspaces = append(workspaces, entry)
			}
			return printSingle(globals, map[string]any{
				"default_workspace": creds.DefaultWorkspace,
				"workspaces":        workspaces,
				"credentials_path":  store.Path(),
			})
		},
	}
	parent.AddCommand(cmd)
}

func registerAuthSetDefault(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "set-default <workspace>",
		Short: "Set the default workspace (accepts an alias, URL, or any --workspace selector)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			if err := store.SetDefault(args[0]); err != nil {
				return mapWorkspaceResolveError(store, args[0], err)
			}
			ws, err := store.Resolve(args[0])
			if err != nil {
				return err
			}
			return printSingle(globals, map[string]any{
				"default_workspace": ws.Alias,
				"workspace_url":     ws.URL,
			})
		},
	}
	parent.AddCommand(cmd)
}

func registerAuthRemove(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "remove <workspace>",
		Short: "Remove a workspace and its stored secrets (accepts an alias, URL, or any --workspace selector)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			// Capture the identity before removal so its cache subtree (resolution
			// cache + downloads + emoji images) can be cleared too — otherwise it
			// would linger as an orphaned directory.
			var cacheKey string
			removed := args[0]
			if ws, rerr := store.Resolve(args[0]); rerr == nil {
				cacheKey = slack.IdentityCacheKey(ws.TeamID, ws.UserID)
				removed = ws.Alias
			}
			if err := store.Remove(args[0]); err != nil {
				return mapWorkspaceResolveError(store, args[0], err)
			}
			result := map[string]any{"removed": removed}
			if cacheKey != "" {
				if err := slack.PurgeIdentityDir(appCacheDir(), cacheKey); err == nil {
					result["cache_cleared"] = true
				}
			}
			return printSingle(globals, result)
		},
	}
	parent.AddCommand(cmd)
}
