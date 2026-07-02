package cli

import (
	"context"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/auth"
	"github.com/shhac/agent-slack/internal/credential"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
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

// saveTeams upserts browser-auth workspaces for the given teams + the cookie
// they share and returns a compact import summary. Imports carry no alias:
// each team updates the entry that uniquely holds its URL or creates one
// under a derived alias; several aliases on one URL is a structured error
// (see mapAmbiguousURLError).
func saveTeams(store *credential.Store, teams []auth.Team, cookieD string, source map[string]string) (map[string]any, error) {
	workspaces := make([]credential.Workspace, 0, len(teams))
	imported := make([]map[string]string, 0, len(teams))
	for _, t := range teams {
		workspaces = append(workspaces, credential.Workspace{
			URL:  t.URL,
			Name: t.Name,
			Auth: credential.Auth{Type: credential.AuthBrowser, XOXC: t.Token, XOXD: cookieD},
		})
		imported = append(imported, map[string]string{"workspace_url": t.URL, "workspace_name": t.Name})
	}
	if err := store.UpsertMany(workspaces); err != nil {
		return nil, mapAmbiguousURLError(err)
	}
	summary := map[string]any{"imported": len(workspaces), "workspaces": imported}
	if len(source) > 0 {
		summary["source"] = source
	}
	return summary, nil
}

// mapAmbiguousURLError turns the store's several-aliases-share-this-URL
// refusal into an agent-actionable error; other errors pass through.
func mapAmbiguousURLError(err error) error {
	var ambiguous *credential.AmbiguousURLError
	if !agenterrors.As(err, &ambiguous) {
		return err
	}
	return agenterrors.Newf(agenterrors.FixableByAgent,
		"several stored workspaces use %s: %s", ambiguous.URL, strings.Join(ambiguous.Aliases, ", ")).
		WithHint("re-run with 'agent-slack auth add --alias <alias>' to say which credential set to update")
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

// runAuthImport is the shared import pipeline: extract, persist, then report.
// --format is already validated by the root PersistentPreRunE, so a bad value
// is rejected before this runs and can't half-import credentials.
func runAuthImport(globals *GlobalFlags, extract func() (*auth.Extracted, error)) error {
	store, err := globals.newStore()
	if err != nil {
		return err
	}
	extracted, err := extract()
	if err != nil {
		return err
	}
	summary, err := saveTeams(store, extracted.Teams, extracted.CookieD, extracted.Source)
	if err != nil {
		return err
	}
	return printSingle(globals, summary)
}

func registerAuthImport(parent *cobra.Command, globals *GlobalFlags, use, short string, extract func() (*auth.Extracted, error)) {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthImport(globals, extract)
		},
	}
	parent.AddCommand(cmd)
}

func registerAuthImportBrowser(parent *cobra.Command, globals *GlobalFlags) {
	var profile string
	browsers := auth.SupportedBrowsers()
	names := make([]string, len(browsers))
	for i, b := range browsers {
		names[i] = b.Name
	}
	cmd := &cobra.Command{
		Use:               "import-browser <browser>",
		Short:             "Import xoxc/xoxd from a browser: " + strings.Join(names, ", "),
		Long:              browserImportLongHelp(browsers),
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: fixedCompletions(names...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthImport(globals, func() (*auth.Extracted, error) {
				return auth.ImportBrowser(args[0], profile)
			})
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "Profile selector (name, directory, or path substring) for Firefox-based browsers")
	parent.AddCommand(cmd)
}

// browserImportLongHelp renders the supported-browser list, marking which
// accept --profile.
func browserImportLongHelp(browsers []auth.BrowserInfo) string {
	var b strings.Builder
	b.WriteString("Import Slack credentials (xoxc tokens + the d cookie) from a browser.\n\nSupported browsers:\n")
	for _, br := range browsers {
		b.WriteString("  " + br.Name)
		if br.SupportsProfile {
			b.WriteString(" [--profile]")
		}
		b.WriteString(" — " + br.Summary + "\n")
	}
	return b.String()
}

func registerAuthParseCurl(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "parse-curl",
		Short: "Read a Slack API request pasted as cURL on stdin and import its xoxc/xoxd",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthImport(globals, func() (*auth.Extracted, error) {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return nil, err
				}
				if strings.TrimSpace(string(raw)) == "" {
					return nil, agenterrors.New("expected a cURL command on stdin", agenterrors.FixableByAgent)
				}
				team, cookieD, err := auth.ParseCurl(string(raw))
				if err != nil {
					return nil, err
				}
				return &auth.Extracted{CookieD: cookieD, Teams: []auth.Team{team}}, nil
			})
		},
	}
	parent.AddCommand(cmd)
}

func registerAuthAdd(parent *cobra.Command, globals *GlobalFlags) {
	var alias, workspaceURL, token, xoxc, xoxd string
	var form bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add credentials directly (standard xoxb/xoxp token, or browser xoxc/xoxd)",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			if form {
				token, xoxc, xoxd, err = promptAddSecrets(cmd.Context(), globals, workspaceURL, token, xoxc, xoxd)
				if err != nil {
					return err
				}
			}
			var ws credential.Workspace
			switch {
			case token != "":
				ws = credential.Workspace{Alias: alias, URL: workspaceURL, Auth: credential.Auth{Type: credential.AuthStandard, Token: token}}
			case xoxc != "" && xoxd != "":
				ws = credential.Workspace{Alias: alias, URL: workspaceURL, Auth: credential.Auth{Type: credential.AuthBrowser, XOXC: xoxc, XOXD: xoxd}}
			default:
				return agenterrors.New("provide either --token or both --xoxc and --xoxd", agenterrors.FixableByAgent).
					WithHint("Agents should use 'auth add --workspace-url <url> --form' so the human types the secret into a native dialog and it never appears in chat.")
			}
			saved, err := store.Upsert(ws)
			if err != nil {
				return mapAmbiguousURLError(err)
			}
			return printSingle(globals, map[string]any{
				"saved":         saved.Alias,
				"workspace_url": saved.URL,
				"auth_type":     string(saved.Auth.Type),
			})
		},
	}
	cmd.Flags().StringVar(&alias, "alias", "", "Alias for this credential set (derived from the workspace when omitted); several aliases may share one workspace URL")
	cmd.Flags().StringVar(&workspaceURL, "workspace-url", "", "Workspace URL, e.g. https://myteam.slack.com")
	cmd.Flags().StringVar(&token, "token", "", "Standard Slack token (xoxb-/xoxp-)")
	cmd.Flags().StringVar(&xoxc, "xoxc", "", "Browser token (xoxc-...)")
	cmd.Flags().StringVar(&xoxd, "xoxd", "", "Browser cookie d (xoxd-...)")
	cmd.Flags().BoolVar(&form, "form", false, "Prompt for missing secrets via a native OS dialog (keeps them out of chat and shell history)")
	_ = cmd.MarkFlagRequired("workspace-url")
	parent.AddCommand(cmd)
}

// promptAddSecrets fills whichever secrets --form still needs via native
// dialogs. A single prompt accepts any token kind; an xoxc- answer routes to a
// follow-up prompt for the xoxd cookie that browser auth also needs.
func promptAddSecrets(ctx context.Context, globals *GlobalFlags, workspaceURL, token, xoxc, xoxd string) (string, string, string, error) {
	title := "agent-slack: " + workspaceURL
	if token == "" && xoxc == "" {
		v, err := globals.promptSecret(ctx, title, "Slack token (xoxb-, xoxp-, or xoxc-)", "")
		if err != nil {
			return "", "", "", err
		}
		if v = strings.TrimSpace(v); strings.HasPrefix(v, "xoxc-") {
			xoxc = v
		} else {
			token = v
		}
	}
	if xoxc != "" && xoxd == "" {
		v, err := globals.promptSecret(ctx, title, "Slack 'd' cookie (xoxd-...)", "")
		if err != nil {
			return "", "", "", err
		}
		xoxd = strings.TrimSpace(v)
	}
	return token, xoxc, xoxd, nil
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
