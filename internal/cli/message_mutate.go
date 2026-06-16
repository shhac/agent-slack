package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerMessageEdit(parent *cobra.Command, globals *GlobalFlags) {
	var ts string
	var yes bool
	var slackMarkdown bool
	var attach []string
	var removeAttachment []string
	cmd := &cobra.Command{
		Use:               "edit <target> [text]",
		Short:             "Edit a message: change text and/or add/remove attachments (destructive: requires --yes)",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			hasText := len(args) == 2
			changingAttachments := len(attach) > 0 || len(removeAttachment) > 0
			if !hasText && !changingAttachments {
				return agenterrors.New("nothing to edit: provide new text and/or --attach/--remove-attachment", agenterrors.FixableByAgent).
					WithHint(`e.g. message edit <target> "new text"  or  message edit <target> --remove-attachment F123`)
			}
			if err := requireYes(yes, describeEdit(args, hasText, attach, removeAttachment)); err != nil {
				return err
			}
			cc, ref, err := resolveMessageTarget(ctx, globals, args[0], ts, "")
			if err != nil {
				return err
			}

			params := map[string]any{
				"channel": ref.ChannelID,
				"ts":      ref.MessageTS,
			}

			// Attachment edits use chat.update file_ids, which replaces the whole
			// set — so start from the message's current attachments, then add and
			// remove. (Omitting file_ids entirely preserves them, the text path.)
			if changingAttachments {
				msg, err := slack.FetchMessage(ctx, cc.Client, ref, false)
				if err != nil {
					return err
				}
				fileIDs, err := editedFileIDs(ctx, cc.Client, msg, attach, removeAttachment)
				if err != nil {
					return err
				}
				params["file_ids"] = strings.Join(fileIDs, ",")
				// Re-send the existing text/blocks so an attachment-only edit
				// doesn't blank the message body.
				if !hasText {
					params["text"] = msg.Text
					if len(msg.Blocks) > 0 {
						params["blocks"] = msg.Blocks
					}
				}
			}

			if hasText {
				text := slack.ResolveMentions(ctx, cc.Client, args[1])
				rtBlocks, outboundText := render.RenderOutbound(text, slackMarkdown)
				params["text"] = render.FormatOutboundText(outboundText)
				if rtBlocks != nil {
					params["blocks"] = toAnySlice(rtBlocks)
				}
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
	cmd.Flags().StringArrayVar(&attach, "attach", nil, "Path to a file to upload and add as an attachment (repeatable)")
	cmd.Flags().StringArrayVar(&removeAttachment, "remove-attachment", nil, "File ID to remove from the message; see the file ids in 'message get' (repeatable)")
	parent.AddCommand(cmd)
}

// describeEdit renders the --yes confirmation line for an edit's combination of
// text rewrite, additions, and removals.
func describeEdit(args []string, hasText bool, attach, removeAttachment []string) string {
	var parts []string
	if hasText {
		parts = append(parts, fmt.Sprintf("rewrite the text (%d chars)", len(args[1])))
	}
	if len(attach) > 0 {
		parts = append(parts, fmt.Sprintf("add %d attachment(s)", len(attach)))
	}
	if len(removeAttachment) > 0 {
		parts = append(parts, fmt.Sprintf("remove %d attachment(s)", len(removeAttachment)))
	}
	return fmt.Sprintf("would %s on the message at %s", strings.Join(parts, ", "), args[0])
}

// editedFileIDs computes the replacement file_ids set for chat.update: the
// message's current attachments, minus removeAttachment, plus freshly uploaded
// attach paths. It errors if a removeAttachment id isn't actually on the message
// so a typo'd removal fails loudly instead of silently no-op'ing.
func editedFileIDs(ctx context.Context, c *slack.Client, msg render.MessageSummary, attach, removeAttachment []string) ([]string, error) {
	remove := map[string]bool{}
	for _, id := range removeAttachment {
		if id := strings.TrimSpace(id); id != "" {
			remove[id] = true
		}
	}

	var ids []string
	matched := map[string]bool{}
	for _, f := range msg.Files {
		if f.ID == "" {
			continue
		}
		if remove[f.ID] {
			matched[f.ID] = true
			continue
		}
		ids = append(ids, f.ID)
	}

	var unknown []string
	for id := range remove {
		if !matched[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, agenterrors.New("message has no attachment(s) with id: "+strings.Join(unknown, ", "), agenterrors.FixableByAgent).
			WithHint("run 'message get <target>' to see the current attachment ids")
	}

	if len(attach) > 0 {
		uploaded, err := c.UploadDraftFiles(ctx, attach)
		if err != nil {
			return nil, err
		}
		ids = append(ids, uploaded...)
	}
	return ids, nil
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
