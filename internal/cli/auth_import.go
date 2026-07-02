// The auth import pipeline: extracting Slack credentials from Slack Desktop,
// browsers, or a pasted cURL request, and persisting the resulting teams.
package cli

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/auth"
	"github.com/shhac/agent-slack/internal/credential"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

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
