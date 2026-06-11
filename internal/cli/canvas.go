package cli

import (
	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/htmlmd"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerCanvas(parent *cobra.Command, globals *GlobalFlags) {
	canvasCmd := &cobra.Command{
		Use:   "canvas",
		Short: "Work with Slack canvases",
	}
	parent.AddCommand(canvasCmd)
	handleUnknownSubcommand(canvasCmd)

	var maxChars int
	getCmd := &cobra.Command{
		Use:   "get <canvas>",
		Short: "Fetch a canvas (…/docs/… URL or F… id) as Markdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			canvasID := args[0]
			var cc *clientContext
			var err error
			if ref, perr := slack.ParseCanvasURL(args[0]); perr == nil {
				canvasID = ref.CanvasID
				cc, err = getClientForWorkspace(globals, ref.WorkspaceURL)
			} else if slack.IsCanvasID(args[0]) {
				cc, err = getClient(globals)
			} else {
				return agenterrors.Newf(agenterrors.FixableByAgent, "not a canvas URL or id: %s", args[0]).
					WithHint("pass a https://…/docs/… link or an F… canvas id")
			}
			if err != nil {
				return err
			}
			canvas, err := slack.FetchCanvasMarkdown(ctx, cc.Client, canvasID, slack.CanvasOptions{
				MaxChars:       maxChars,
				DownloadsDir:   downloadsDir(),
				HTMLToMarkdown: htmlmd.Convert,
			})
			if err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"canvas": canvas})
		},
	}
	getCmd.Flags().IntVar(&maxChars, "max-chars", 20000, "Max markdown chars (-1 = unlimited)")
	canvasCmd.AddCommand(getCmd)
}
