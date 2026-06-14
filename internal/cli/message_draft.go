package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// registerMessageDraft is the `message draft` group — the LLM→human hand-off.
// Plain drafts are one-per-target (Slack enforces it), so the lifecycle is
// target-addressed: create/list/get/edit/delete/send. Browser auth only;
// scheduled messages live under `message scheduled`, not here.
func registerMessageDraft(parent *cobra.Command, globals *GlobalFlags) {
	draftCmd := &cobra.Command{
		Use:   "draft",
		Short: "Save and manage drafts (browser auth) — a hand-off for the user to review and send",
	}
	parent.AddCommand(draftCmd)
	handleUnknownSubcommand(draftCmd)

	registerDraftCreate(draftCmd, globals)
	registerDraftList(draftCmd, globals)
	registerDraftGet(draftCmd, globals)
	registerDraftEdit(draftCmd, globals)
	registerDraftDelete(draftCmd, globals)
	registerDraftSend(draftCmd, globals)
}

func registerDraftCreate(parent *cobra.Command, globals *GlobalFlags) {
	var blocksPath string
	var slackMarkdown bool
	cmd := &cobra.Command{
		Use:               "create <target> [text]",
		Short:             "Save a draft for the user to review, edit, and send",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			req, cc, err := buildDraftRequest(ctx, cmd, globals, args, blocksPath, slackMarkdown)
			if err != nil {
				return err
			}
			d, err := slack.SaveDraft(ctx, cc.Client, req.outgoing(), 0)
			if err != nil {
				if slack.ErrorCode(err) == "attached_draft_exists" {
					return agenterrors.Newf(agenterrors.FixableByAgent, "a draft already exists for %s", args[0]).
						WithHint("edit it with 'message draft edit " + args[0] + " …' or remove it with 'message draft delete " + args[0] + "'")
				}
				return err
			}
			return printSingle(globals, draftPayload(d, "saved as a draft — open Slack to review, edit, and send"))
		},
	}
	cmd.Flags().StringVar(&blocksPath, "blocks", "", "Path to a JSON file with Block Kit blocks ('-' = stdin)")
	cmd.Flags().BoolVar(&slackMarkdown, "slack-markdown", false, "Interpret text as Slack mrkdwn instead of standard Markdown")
	parent.AddCommand(cmd)
}

func registerDraftList(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List plain (unscheduled) drafts; scheduled messages are under 'message scheduled list'",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			if err := requireDraftAuth(cc); err != nil {
				return err
			}
			drafts, err := slack.ListDrafts(ctx, cc.Client)
			if err != nil {
				return err
			}
			items := make([]any, len(drafts))
			for i, d := range drafts {
				items[i] = draftItem(d)
			}
			return printList(globals, items, nil)
		},
	}
	parent.AddCommand(cmd)
}

func registerDraftGet(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:               "get <target>",
		Short:             "Show the plain draft for a target",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, d, err := resolveTargetDraft(cmd.Context(), globals, args[0])
			if err != nil {
				return err
			}
			return printSingle(globals, draftItem(d))
		},
	}
	parent.AddCommand(cmd)
}

func registerDraftEdit(parent *cobra.Command, globals *GlobalFlags) {
	var blocksPath string
	var slackMarkdown bool
	cmd := &cobra.Command{
		Use:               "edit <target> [text]",
		Short:             "Replace the plain draft for a target",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			req, cc, err := buildDraftRequest(ctx, cmd, globals, args, blocksPath, slackMarkdown)
			if err != nil {
				return err
			}
			d, ok, err := slack.PlainDraftForChannel(ctx, cc.Client, req.channelID)
			if err != nil {
				return err
			}
			if !ok {
				return agenterrors.Newf(agenterrors.FixableByAgent, "no draft for %s", args[0]).
					WithHint("create one with 'message draft create " + args[0] + " …'; scheduled messages are under 'message scheduled list'")
			}
			req.channelID = d.ChannelID
			updated, err := slack.UpdateDraft(ctx, cc.Client, d.ID, req.outgoing(), 0)
			if err != nil {
				return err
			}
			return printSingle(globals, draftPayload(updated, "draft updated"))
		},
	}
	cmd.Flags().StringVar(&blocksPath, "blocks", "", "Path to a JSON file with Block Kit blocks ('-' = stdin)")
	cmd.Flags().BoolVar(&slackMarkdown, "slack-markdown", false, "Interpret text as Slack mrkdwn instead of standard Markdown")
	parent.AddCommand(cmd)
}

func registerDraftDelete(parent *cobra.Command, globals *GlobalFlags) {
	var yes bool
	cmd := &cobra.Command{
		Use:               "delete <target>",
		Short:             "Discard the plain draft for a target (destructive: requires --yes)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, d, err := resolveTargetDraft(ctx, globals, args[0])
			if err != nil {
				return err
			}
			if err := requireYes(yes, "would discard the draft for "+args[0]); err != nil {
				return err
			}
			if err := slack.DeleteDraft(ctx, cc.Client, d.ID); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"ok": true, "draft_id": d.ID, "channel_id": d.ChannelID})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm discarding the draft")
	parent.AddCommand(cmd)
}

func registerDraftSend(parent *cobra.Command, globals *GlobalFlags) {
	var schedule, scheduleIn string
	cmd := &cobra.Command{
		Use:               "send <target>",
		Short:             "Send the plain draft for a target now, or --schedule/--schedule-in to promote it to a scheduled message",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			postAt, err := slack.ResolveSchedulePostAt(schedule, scheduleIn, time.Now())
			if err != nil {
				return err
			}
			cc, d, err := resolveTargetDraft(ctx, globals, args[0])
			if err != nil {
				return err
			}
			msg := slack.OutgoingMessage{ChannelID: d.ChannelID, Blocks: d.Blocks}
			if postAt != 0 {
				// Promote the plain draft to a scheduled message in place (same id);
				// no separate post/delete — Slack delivers it at post_at.
				promoted, err := slack.UpdateDraft(ctx, cc.Client, d.ID, msg, postAt)
				if err != nil {
					return err
				}
				// Prefer the time Slack echoes; fall back to the requested time when
				// the update response omits date_scheduled.
				scheduledAt := promoted.PostAt
				if scheduledAt == 0 {
					scheduledAt = postAt
				}
				payload := scheduleResultPayload(
					slack.ScheduleResult{ChannelID: promoted.ChannelID, ScheduledMessageID: promoted.ID, PostAt: scheduledAt}, "")
				payload["note"] = "promoted the draft to a scheduled message — manage it under 'message scheduled'"
				return printSingle(globals, payload)
			}
			result, err := slack.PostMessage(ctx, cc.Client, msg)
			if err != nil {
				return err
			}
			// Best effort: the message is sent; a stale draft is harmless.
			_ = slack.DeleteDraft(ctx, cc.Client, d.ID)
			return printSingle(globals, postedMessagePayload(result, cc.WorkspaceURL, ""))
		},
	}
	cmd.Flags().StringVar(&schedule, "schedule", "", "Promote to a scheduled message at an ISO 8601 time with timezone, or a unix timestamp")
	cmd.Flags().StringVar(&scheduleIn, "schedule-in", "", "Promote to a scheduled message after a duration (30m, 2d, tomorrow 9am, monday 9am)")
	parent.AddCommand(cmd)
}

// --- shared draft helpers ---------------------------------------------------

// buildDraftRequest parses the target and validates the text/--blocks into a
// sendRequest (reusing the send build path, minus scheduling/attachments).
func buildDraftRequest(ctx context.Context, cmd *cobra.Command, globals *GlobalFlags, args []string, blocksPath string, slackMarkdown bool) (sendRequest, *clientContext, error) {
	target, err := render.ParseTarget(args[0])
	if err != nil {
		return sendRequest{}, nil, err
	}
	cc, channelID, err := resolveDraftClient(ctx, globals, target)
	if err != nil {
		return sendRequest{}, nil, err
	}
	text := ""
	if len(args) > 1 {
		text = args[1]
	}
	text = slack.ResolveMentions(ctx, cc.Client, text)
	req, err := buildSendRequest(cmd.InOrStdin(), target.Kind, text, sendFlags{blocksPath: blocksPath, slackMarkdown: slackMarkdown}, time.Now())
	if err != nil {
		return sendRequest{}, nil, err
	}
	req.channelID = channelID
	return req, cc, nil
}

// resolveDraftClient resolves a target to a browser-auth client + channel id.
func resolveDraftClient(ctx context.Context, globals *GlobalFlags, target render.Target) (*clientContext, string, error) {
	cc, channelID, err := resolveTargetClient(ctx, globals, target, "")
	if err != nil {
		return nil, "", err
	}
	if err := requireDraftAuth(cc); err != nil {
		return nil, "", err
	}
	return cc, channelID, nil
}

// resolveTargetDraft resolves a target to its single plain draft (browser auth),
// erroring with a create hint when the target has no plain draft.
func resolveTargetDraft(ctx context.Context, globals *GlobalFlags, targetArg string) (*clientContext, slack.Draft, error) {
	target, err := render.ParseTarget(targetArg)
	if err != nil {
		return nil, slack.Draft{}, err
	}
	cc, channelID, err := resolveDraftClient(ctx, globals, target)
	if err != nil {
		return nil, slack.Draft{}, err
	}
	d, ok, err := slack.PlainDraftForChannel(ctx, cc.Client, channelID)
	if err != nil {
		return nil, slack.Draft{}, err
	}
	if !ok {
		return nil, slack.Draft{}, agenterrors.Newf(agenterrors.FixableByAgent, "no draft for %s", targetArg).
			WithHint("create one with 'message draft create " + targetArg + " …'; scheduled messages are under 'message scheduled list'")
	}
	return cc, d, nil
}

func requireDraftAuth(cc *clientContext) error {
	if cc.AuthType == slack.AuthBrowser {
		return nil
	}
	return agenterrors.New("drafts require browser auth (xoxc/xoxd); they are a client feature, not available with a bot/user token", agenterrors.FixableByHuman).
		WithHint("import browser credentials with 'agent-slack auth import-desktop'")
}

func draftItem(d slack.Draft) map[string]any {
	item := map[string]any{"id": d.ID, "channel_id": d.ChannelID, "text": d.Text}
	if d.PostAt > 0 {
		item["post_at"] = d.PostAt
	}
	return item
}

func draftPayload(d slack.Draft, note string) map[string]any {
	return map[string]any{"ok": true, "draft_id": d.ID, "channel_id": d.ChannelID, "note": note}
}
