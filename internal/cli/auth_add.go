// auth add: direct credential entry, with the three secret sources (flags,
// stdin JSON, native dialog) funneled through one addSecrets triple.
package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/credential"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

func registerAuthAdd(parent *cobra.Command, globals *GlobalFlags) {
	var alias, workspaceURL string
	var in addSecrets
	var form, stdinSecrets bool
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add credentials directly (standard xoxb/xoxp token, or browser xoxc/xoxd)",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := globals.newStore()
			if err != nil {
				return err
			}
			if stdinSecrets {
				if in, err = readStdinSecrets(cmd.InOrStdin(), in); err != nil {
					return err
				}
			}
			if form {
				if in, err = promptAddSecrets(cmd.Context(), globals, workspaceURL, in); err != nil {
					return err
				}
			}
			ws, err := in.workspace(alias, workspaceURL)
			if err != nil {
				return err
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
	cmd.Flags().StringVar(&in.token, "token", "", "Standard Slack token (xoxb-/xoxp-)")
	cmd.Flags().StringVar(&in.xoxc, "xoxc", "", "Browser token (xoxc-...)")
	cmd.Flags().StringVar(&in.xoxd, "xoxd", "", "Browser cookie d (xoxd-...)")
	cmd.Flags().BoolVar(&form, "form", false, "Prompt for missing secrets via a native OS dialog (keeps them out of chat and shell history)")
	cmd.Flags().BoolVar(&stdinSecrets, "stdin", false, "Read secrets as one JSON object on stdin: {\"token\": …} or {\"xoxc\": …, \"xoxd\": …} (keeps them out of argv and process env)")
	_ = cmd.MarkFlagRequired("workspace-url")
	parent.AddCommand(cmd)
}

// addSecrets is the secret triple auth add gathers — from flags, stdin, and
// the native dialog, in that precedence. Exactly one shape is valid: a
// standard token, or the browser xoxc+xoxd pair.
type addSecrets struct {
	token, xoxc, xoxd string
}

// workspace classifies the gathered secrets into a Workspace, or returns the
// agent-fixable error when neither shape is complete.
func (in addSecrets) workspace(alias, workspaceURL string) (credential.Workspace, error) {
	switch {
	case in.token != "":
		return credential.Workspace{Alias: alias, URL: workspaceURL,
			Auth: credential.Auth{Type: credential.AuthStandard, Token: in.token}}, nil
	case in.xoxc != "" && in.xoxd != "":
		return credential.Workspace{Alias: alias, URL: workspaceURL,
			Auth: credential.Auth{Type: credential.AuthBrowser, XOXC: in.xoxc, XOXD: in.xoxd}}, nil
	default:
		return credential.Workspace{}, agenterrors.New("provide either --token or both --xoxc and --xoxd", agenterrors.FixableByAgent).
			WithHint("Agents should use 'auth add --workspace-url <url> --form' so the human types the secret into a native dialog and it never appears in chat.")
	}
}

// readStdinSecrets fills whichever secrets --stdin still needs from a single
// JSON object — the machine path for secret entry (web enrollment, scripts),
// where argv is ps-visible and env is inherited by children. Explicit flags
// win over stdin values.
func readStdinSecrets(r io.Reader, in addSecrets) (addSecrets, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return in, err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return in, agenterrors.New("expected a JSON object with secrets on stdin", agenterrors.FixableByAgent).
			WithHint(`pipe {"token": "xoxb-…"} or {"xoxc": "xoxc-…", "xoxd": "xoxd-…"} into 'auth add --stdin'`)
	}
	var parsed struct {
		Token string `json:"token"`
		XOXC  string `json:"xoxc"`
		XOXD  string `json:"xoxd"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return in, agenterrors.Wrap(err, agenterrors.FixableByAgent).
			WithHint(`stdin must be one JSON object, e.g. {"token": "xoxb-…"}`)
	}
	if in.token == "" {
		in.token = strings.TrimSpace(parsed.Token)
	}
	if in.xoxc == "" {
		in.xoxc = strings.TrimSpace(parsed.XOXC)
	}
	if in.xoxd == "" {
		in.xoxd = strings.TrimSpace(parsed.XOXD)
	}
	return in, nil
}

// promptAddSecrets fills whichever secrets --form still needs via native
// dialogs. A single prompt accepts any token kind; an xoxc- answer routes to a
// follow-up prompt for the xoxd cookie that browser auth also needs.
func promptAddSecrets(ctx context.Context, globals *GlobalFlags, workspaceURL string, in addSecrets) (addSecrets, error) {
	title := "agent-slack: " + workspaceURL
	if in.token == "" && in.xoxc == "" {
		v, err := globals.promptSecret(ctx, title, "Slack token (xoxb-, xoxp-, or xoxc-)", "")
		if err != nil {
			return in, err
		}
		if v = strings.TrimSpace(v); strings.HasPrefix(v, "xoxc-") {
			in.xoxc = v
		} else {
			in.token = v
		}
	}
	if in.xoxc != "" && in.xoxd == "" {
		v, err := globals.promptSecret(ctx, title, "Slack 'd' cookie (xoxd-...)", "")
		if err != nil {
			return in, err
		}
		in.xoxd = strings.TrimSpace(v)
	}
	return in, nil
}
