package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerMessageEdit(parent *cobra.Command, globals *GlobalFlags) {
	var ts string
	var yes bool
	var slackMarkdown bool
	cmd := &cobra.Command{
		Use:               "edit <target> <text>",
		Short:             "Edit a message (destructive: requires --yes)",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := requireYes(yes, fmt.Sprintf("would rewrite the message at %s with %d chars of new text", args[0], len(args[1]))); err != nil {
				return err
			}
			cc, ref, err := resolveMessageTarget(ctx, globals, args[0], ts, "")
			if err != nil {
				return err
			}
			text := slack.ResolveMentions(ctx, cc.Client, args[1])
			rtBlocks, outboundText := render.RenderOutbound(text, slackMarkdown)
			params := map[string]any{
				"channel": ref.ChannelID,
				"ts":      ref.MessageTS,
				"text":    render.FormatOutboundText(outboundText),
			}
			if rtBlocks != nil {
				params["blocks"] = toAnySlice(rtBlocks)
			}
			if _, err := cc.Client.API(ctx, "chat.update", params); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"ok": true})
		},
	}
	cmd.Flags().StringVar(&ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm the edit")
	cmd.Flags().BoolVar(&slackMarkdown, "slack-markdown", false, "Interpret text as Slack mrkdwn instead of standard Markdown")
	parent.AddCommand(cmd)
}

func registerMessageDelete(parent *cobra.Command, globals *GlobalFlags) {
	var ts string
	var yes bool
	cmd := &cobra.Command{
		Use:               "delete <target>",
		Short:             "Delete a message (destructive: requires --yes)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := requireYes(yes, fmt.Sprintf("would permanently delete the message at %s", args[0])); err != nil {
				return err
			}
			cc, ref, err := resolveMessageTarget(ctx, globals, args[0], ts, "")
			if err != nil {
				return err
			}
			if _, err := cc.Client.API(ctx, "chat.delete", map[string]any{
				"channel": ref.ChannelID,
				"ts":      ref.MessageTS,
			}); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"ok": true})
		},
	}
	cmd.Flags().StringVar(&ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm the delete")
	parent.AddCommand(cmd)
}

func registerMessageReact(parent *cobra.Command, globals *GlobalFlags) {
	reactCmd := &cobra.Command{
		Use:   "react",
		Short: "Add or remove emoji reactions",
	}
	parent.AddCommand(reactCmd)
	handleUnknownSubcommand(reactCmd)
	for _, action := range []string{"add", "remove"} {
		var ts string
		cmd := &cobra.Command{
			Use:   action + " <target> <emoji>",
			Short: strings.ToUpper(action[:1]) + action[1:] + " a reaction (:rocket:, rocket, or 🚀)",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				name, err := render.NormalizeReactionName(args[1])
				if err != nil {
					return err
				}
				cc, ref, err := resolveMessageTarget(ctx, globals, args[0], ts, "")
				if err != nil {
					return err
				}
				method := "reactions." + cmd.Name()
				if _, err := cc.Client.API(ctx, method, map[string]any{
					"channel":   ref.ChannelID,
					"timestamp": ref.MessageTS,
					"name":      name,
				}); err != nil {
					return err
				}
				return printSingle(globals, map[string]any{"ok": true})
			},
		}
		cmd.Flags().StringVar(&ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
		reactCmd.AddCommand(cmd)
	}
}
