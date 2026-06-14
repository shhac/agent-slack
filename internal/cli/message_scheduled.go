package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerMessageScheduled(parent *cobra.Command, globals *GlobalFlags) {
	scheduledCmd := &cobra.Command{
		Use:   "scheduled",
		Short: "Pending scheduled messages",
	}
	parent.AddCommand(scheduledCmd)
	handleUnknownSubcommand(scheduledCmd)

	var channel, cursor, oldest, latest string
	var limit int
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List pending scheduled messages",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, channelID, err := resolveScheduledChannel(ctx, globals, channel)
			if err != nil {
				return err
			}
			page, err := slack.ListScheduledMessages(ctx, cc.Client, slack.ScheduledListOptions{
				ChannelID: channelID,
				Cursor:    cursor,
				Oldest:    oldest,
				Latest:    latest,
				Limit:     limit,
			})
			if err != nil {
				return err
			}
			return printList(globals, toAnySlice(page.ScheduledMessages), listMeta(page.NextCursor, nil))
		},
	}
	listCmd.Flags().StringVar(&channel, "channel", "", "Limit to a channel/DM (id, #name, or U… for a DM)")
	registerFlagCompletion(listCmd, "channel", globals, slack.CompleteChannels|slack.CompleteUsers)
	listCmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor")
	listCmd.Flags().StringVar(&oldest, "oldest", "", "Only messages scheduled after this unix time")
	listCmd.Flags().StringVar(&latest, "latest", "", "Only messages scheduled before this unix time")
	listCmd.Flags().IntVar(&limit, "limit", 0, "Max scheduled messages to return")
	scheduledCmd.AddCommand(listCmd)

	var cancelChannel string
	var yes bool
	cancelCmd := &cobra.Command{
		Use:   "cancel <scheduled-message-id>",
		Short: "Cancel a pending scheduled message (destructive: requires --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, channelID, err := resolveScheduledChannel(ctx, globals, cancelChannel)
			if err != nil {
				return err
			}
			// Browser auth cancels a draft by its globally-unique id; a bot/user
			// token needs the channel for chat.deleteScheduledMessage.
			if cc.AuthType != slack.AuthBrowser && cancelChannel == "" {
				return agenterrors.New("--channel is required", agenterrors.FixableByAgent)
			}
			if err := requireYes(yes, fmt.Sprintf("would cancel scheduled message %s", args[0])); err != nil {
				return err
			}
			if err := slack.CancelScheduledMessage(ctx, cc.Client, channelID, args[0]); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{
				"ok":                   true,
				"channel_id":           channelID,
				"scheduled_message_id": args[0],
			})
		},
	}
	cancelCmd.Flags().StringVar(&cancelChannel, "channel", "", "Channel/DM the message was scheduled for (required)")
	registerFlagCompletion(cancelCmd, "channel", globals, slack.CompleteChannels|slack.CompleteUsers)
	cancelCmd.Flags().BoolVar(&yes, "yes", false, "Confirm the cancellation")
	scheduledCmd.AddCommand(cancelCmd)
}

// resolveScheduledChannel maps a --channel value (permalink, #name, C…, or
// U… DM target) to a client + channel ID. Empty input means "all channels".
func resolveScheduledChannel(ctx context.Context, globals *GlobalFlags, channel string) (*clientContext, string, error) {
	if strings.TrimSpace(channel) == "" {
		cc, err := getClient(globals)
		return cc, "", err
	}
	target, err := render.ParseTarget(channel)
	if err != nil {
		return nil, "", err
	}
	return resolveTargetClient(ctx, globals, target, "")
}
