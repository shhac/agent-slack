package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/output"
)

func registerAPI(parent *cobra.Command, globals *GlobalFlags) {
	apiCmd := &cobra.Command{
		Use:   "api",
		Short: "Raw Slack API escape hatch",
	}
	parent.AddCommand(apiCmd)
	handleUnknownSubcommand(apiCmd)

	var params string
	var multipart bool
	callCmd := &cobra.Command{
		Use:   "call <method>",
		Short: "POST any Slack API method with stored credentials and print the raw response",
		Long: `POST any Slack Web API method with the workspace's stored credentials.
Prefer the wrapped commands when one exists — they emit compact, chainable
output. This is for endpoints agent-slack does not cover.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			method := strings.TrimSpace(args[0])
			if method == "" || strings.ContainsAny(method, "/? ") {
				return agenterrors.Newf(agenterrors.FixableByAgent, "invalid Slack API method: %q", args[0]).
					WithHint("pass a bare method name like conversations.history")
			}

			callParams := map[string]any{}
			if params != "" {
				var raw []byte
				var err error
				if params == "-" {
					raw, err = io.ReadAll(cmd.InOrStdin())
				} else if strings.HasPrefix(strings.TrimSpace(params), "{") {
					raw = []byte(params)
				} else {
					raw, err = os.ReadFile(params)
				}
				if err != nil {
					return agenterrors.Newf(agenterrors.FixableByAgent, "--params: %v", err)
				}
				if err := json.Unmarshal(raw, &callParams); err != nil {
					return agenterrors.Newf(agenterrors.FixableByAgent, "--params must be a JSON object: %v", err)
				}
			}

			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			var resp map[string]any
			if multipart {
				resp, err = cc.Client.APIMultipart(ctx, method, callParams)
			} else {
				resp, err = cc.Client.API(ctx, method, callParams)
			}
			if err != nil {
				return err
			}
			format, err := resolveFormat(globals, output.FormatJSON)
			if err != nil {
				return err
			}
			output.Print(globals.stdout, resp, format, false) // raw response: no pruning
			return nil
		},
	}
	callCmd.Flags().StringVar(&params, "params", "", "Params as inline JSON, a file path, or '-' for stdin")
	callCmd.Flags().BoolVar(&multipart, "multipart", false, "Send multipart/form-data (some internal methods require it)")
	apiCmd.AddCommand(callCmd)
}
