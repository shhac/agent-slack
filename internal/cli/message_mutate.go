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
			text := ""
			if hasText {
				text = args[1]
			}
			params, err := buildEditParams(ctx, cc.Client, ref, text, hasText, slackMarkdown, attach, removeAttachment)
			if err != nil {
				return err
			}
			if _, err := cc.Client.API(ctx, "chat.update", params); err != nil {
				return err
			}
			return printOK(globals)
		},
	}
	cmd.Flags().StringVar(&ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm the edit")
	cmd.Flags().BoolVar(&slackMarkdown, "slack-markdown", false, "Interpret text as Slack mrkdwn instead of standard Markdown")
	cmd.Flags().StringArrayVar(&attach, "attach", nil, "Path to a file to upload and add as an attachment (repeatable)")
	cmd.Flags().StringArrayVar(&removeAttachment, "remove-attachment", nil, "File ID to remove from the message; see the file ids in 'message get' (repeatable)")
	parent.AddCommand(cmd)
}

// buildEditParams assembles the chat.update params for an edit. Attachment
// edits use file_ids, which replaces the whole set — so it reads the message's
// current attachments and adds/removes against them, re-sending the existing
// body when only attachments change so the text isn't blanked. A text-only edit
// omits file_ids entirely, which preserves existing attachments.
func buildEditParams(ctx context.Context, c *slack.Client, ref *render.MessageRef, text string, hasText, slackMarkdown bool, attach, removeAttachment []string) (map[string]any, error) {
	params := map[string]any{
		"channel": ref.ChannelID,
		"ts":      ref.MessageTS,
	}

	if len(attach) > 0 || len(removeAttachment) > 0 {
		msg, err := slack.FetchMessage(ctx, c, ref, false)
		if err != nil {
			return nil, err
		}
		fileIDs, err := editedFileIDs(ctx, c, msg, attach, removeAttachment)
		if err != nil {
			return nil, err
		}
		params["file_ids"] = strings.Join(fileIDs, ",")
		if !hasText {
			params["text"] = msg.Text
			if len(msg.Blocks) > 0 {
				params["blocks"] = msg.Blocks
			}
		}
	}

	if hasText {
		resolved := slack.ResolveMentions(ctx, c, text)
		resolved = slack.ResolveChannelMentions(ctx, c, resolved)
		outboundText, blocks := outboundTextAndBlocks(resolved, slackMarkdown, ref.WorkspaceURL)
		params["text"] = outboundText
		if blocks != nil {
			params["blocks"] = blocks
		}
	}

	return params, nil
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
			return printOK(globals)
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
				return printOK(globals)
			},
		}
		cmd.Flags().StringVar(&ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
		reactCmd.AddCommand(cmd)
	}
}
