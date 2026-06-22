package cli

import (
	libcli "github.com/shhac/lib-agent-cli/cli"
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/slack"
)

func registerUnreads(parent *cobra.Command, globals *GlobalFlags) {
	var countsOnly, includeSystem, slackMarkdown bool
	var maxMessages, maxBodyChars int
	tflags := &transcriptFlags{}
	cmd := &cobra.Command{
		Use:   "unreads",
		Short: "Show unread messages across channels, DMs, and threads",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			result, err := slack.FetchUnreads(cmd.Context(), cc.Client, slack.UnreadsOptions{
				IncludeMessages:       !countsOnly,
				MaxMessagesPerChannel: maxMessages,
				MaxBodyChars:          maxBodyChars,
				SkipSystemMessages:    !includeSystem,
				SlackMarkdown:         slackMarkdown,
			})
			if err != nil {
				return err
			}
			if wantsTranscript(globals) {
				return renderUnreadsTranscript(cmd.Context(), globals, cc, tflags, result.Channels)
			}
			var extra map[string]any
			if result.Threads != nil {
				extra = map[string]any{"threads": result.Threads}
			}
			return printList(globals, toAnySlice(result.Channels), listMeta("", extra))
		},
	}
	tflags.register(cmd)
	libcli.AllowFormats(cmd, transcriptFormat)
	cmd.Flags().BoolVar(&countsOnly, "counts-only", false, "Only unread counts, no message content")
	cmd.Flags().IntVar(&maxMessages, "max-messages", 10, "Max unread messages per channel")
	cmd.Flags().IntVar(&maxBodyChars, "max-body-chars", 4000, "Max content chars per message (-1 = unlimited)")
	cmd.Flags().BoolVar(&includeSystem, "include-system", false, "Include system messages (joins, topic changes, …)")
	cmd.Flags().BoolVar(&slackMarkdown, "slack-markdown", false, "Render content as Slack mrkdwn instead of standard Markdown")
	parent.AddCommand(cmd)
}
