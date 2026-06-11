package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/slack"
)

func registerUnreads(parent *cobra.Command, globals *GlobalFlags) {
	var countsOnly, includeSystem bool
	var maxMessages, maxBodyChars int
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
			})
			if err != nil {
				return err
			}
			meta := listMeta(metaEntry("threads", result.Threads, result.Threads == nil))
			return printList(globals, toAnySlice(result.Channels), meta)
		},
	}
	cmd.Flags().BoolVar(&countsOnly, "counts-only", false, "Only unread counts, no message content")
	cmd.Flags().IntVar(&maxMessages, "max-messages", 10, "Max unread messages per channel")
	cmd.Flags().IntVar(&maxBodyChars, "max-body-chars", 4000, "Max content chars per message (-1 = unlimited)")
	cmd.Flags().BoolVar(&includeSystem, "include-system", false, "Include system messages (joins, topic changes, …)")
	parent.AddCommand(cmd)
}
