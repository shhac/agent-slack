package cli

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/auth"
	"github.com/shhac/agent-slack/internal/credential"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/output"
)

func registerAuth(parent *cobra.Command, globals *GlobalFlags) {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage Slack credentials (import-only; tokens are stored in the macOS Keychain)",
	}
	parent.AddCommand(authCmd)
	handleUnknownSubcommand(authCmd)

	registerAuthWhoami(authCmd, globals)
	registerAuthTest(authCmd, globals)
	registerAuthImport(authCmd, globals, "import-desktop",
		"Import xoxc tokens + the d cookie from Slack Desktop (no need to quit Slack)",
		auth.ExtractFromSlackDesktop)
	registerAuthImport(authCmd, globals, "import-chrome",
		"Import xoxc/xoxd from a logged-in Slack tab in Google Chrome (macOS)",
		auth.ExtractFromChrome)
	registerAuthImport(authCmd, globals, "import-brave",
		"Import xoxc/xoxd from a logged-in Slack tab in Brave (macOS)",
		auth.ExtractFromBrave)
	registerAuthImportFirefox(authCmd, globals)
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

// saveTeams upserts browser-auth workspaces for the given teams + shared cookie
// and returns a compact import summary.
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
		return nil, err
	}
	summary := map[string]any{"imported": len(workspaces), "workspaces": imported}
	if len(source) > 0 {
		summary["source"] = source
	}
	return summary, nil
}

func registerAuthWhoami(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show configured workspaces and token sources (secrets redacted)",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			creds, err := store.Load()
			if err != nil {
				return err
			}
			workspaces := make([]map[string]any, 0, len(creds.Workspaces))
			for _, w := range creds.Workspaces {
				entry := map[string]any{
					"workspace_url":  w.URL,
					"workspace_name": w.Name,
					"auth_type":      string(w.Auth.Type),
				}
				if w.Auth.Type == credential.AuthStandard {
					entry["token"] = credential.Redact(w.Auth.Token)
				} else {
					entry["token"] = credential.Redact(w.Auth.XOXC)
					entry["cookie_d"] = credential.Redact(w.Auth.XOXD)
				}
				workspaces = append(workspaces, entry)
			}
			return printSingle(globals, map[string]any{
				"default_workspace_url": creds.DefaultWorkspaceURL,
				"workspaces":            workspaces,
				"credentials_path":      store.Path(),
			})
		},
	}
	parent.AddCommand(cmd)
}

// runAuthImport is the shared import pipeline. The output format is
// validated after extraction but before anything persists, so a bad --format
// never half-imports credentials.
func runAuthImport(globals *GlobalFlags, extract func() (*auth.Extracted, error)) error {
	store, err := globals.newStore()
	if err != nil {
		return err
	}
	extracted, err := extract()
	if err != nil {
		return err
	}
	format, err := resolveFormat(globals, output.FormatJSON)
	if err != nil {
		return err
	}
	summary, err := saveTeams(store, extracted.Teams, extracted.CookieD, extracted.Source)
	if err != nil {
		return err
	}
	output.Print(globals.stdout, summary, format, true)
	return nil
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

func registerAuthImportFirefox(parent *cobra.Command, globals *GlobalFlags) {
	var profile string
	cmd := &cobra.Command{
		Use:   "import-firefox",
		Short: "Import xoxc/xoxd from a Firefox profile (macOS/Linux)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthImport(globals, func() (*auth.Extracted, error) {
				return auth.ExtractFromFirefox(profile)
			})
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "Firefox profile name, directory, or path substring to select")
	parent.AddCommand(cmd)
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
	var workspaceURL, token, xoxc, xoxd string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add credentials directly (standard xoxb/xoxp token, or browser xoxc/xoxd)",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			var ws credential.Workspace
			switch {
			case token != "":
				ws = credential.Workspace{URL: workspaceURL, Auth: credential.Auth{Type: credential.AuthStandard, Token: token}}
			case xoxc != "" && xoxd != "":
				ws = credential.Workspace{URL: workspaceURL, Auth: credential.Auth{Type: credential.AuthBrowser, XOXC: xoxc, XOXD: xoxd}}
			default:
				return agenterrors.New("provide either --token or both --xoxc and --xoxd", agenterrors.FixableByAgent)
			}
			saved, err := store.Upsert(ws)
			if err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"saved": saved.URL, "auth_type": string(saved.Auth.Type)})
		},
	}
	cmd.Flags().StringVar(&workspaceURL, "workspace-url", "", "Workspace URL, e.g. https://myteam.slack.com")
	cmd.Flags().StringVar(&token, "token", "", "Standard Slack token (xoxb-/xoxp-)")
	cmd.Flags().StringVar(&xoxc, "xoxc", "", "Browser token (xoxc-...)")
	cmd.Flags().StringVar(&xoxd, "xoxd", "", "Browser cookie d (xoxd-...)")
	_ = cmd.MarkFlagRequired("workspace-url")
	parent.AddCommand(cmd)
}

func registerAuthSetDefault(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "set-default <workspace-url>",
		Short: "Set the default workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			if err := store.SetDefault(args[0]); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"default_workspace_url": args[0]})
		},
	}
	parent.AddCommand(cmd)
}

func registerAuthRemove(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "remove <workspace-url>",
		Short: "Remove a workspace and its stored secrets",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			if err := store.Remove(args[0]); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"removed": args[0]})
		},
	}
	parent.AddCommand(cmd)
}
