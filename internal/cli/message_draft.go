package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// registerMessageDraft is the `message draft` group — the LLM→human hand-off.
// Drafts are many-per-target and non-intrusive (see the slack package note), so
// create returns an id and get/edit/delete/send address a draft by id, or by a
// target when it holds exactly one. Browser auth only; scheduled messages live
// under `message scheduled`, not here.
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
	var forward string
	var attach []string
	var threadTS string
	cmd := &cobra.Command{
		Use:               "create <target> [text]",
		Short:             "Save a draft for the user to review, edit, and send (returns its id)",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			req, cc, err := buildDraftRequest(ctx, cmd, globals, args, blocksPath, slackMarkdown, forward, attach, threadTS)
			if err != nil {
				return err
			}
			d, err := slack.SaveDraft(ctx, cc.Client, req.outgoing(), 0)
			if err != nil {
				return err
			}
			return printSingle(globals, draftPayload(d, "saved as a draft — open Slack to review, edit, and send (address it by id; a target may hold several drafts)"))
		},
	}
	cmd.Flags().StringVar(&blocksPath, "blocks", "", "Path to a JSON file with Block Kit blocks ('-' = stdin)")
	cmd.Flags().BoolVar(&slackMarkdown, "slack-markdown", false, "Interpret text as Slack mrkdwn instead of standard Markdown")
	cmd.Flags().StringVar(&forward, "forward", "", "Forward a message: a Slack permalink whose message is embedded (text becomes an optional comment; same workspace only)")
	cmd.Flags().StringArrayVar(&attach, "attach", nil, "Attach a local file to the draft (repeatable)")
	cmd.Flags().StringVar(&threadTS, "thread-ts", "", "Reply in a thread: the thread root ts (or pass a message permalink as the target)")
	parent.AddCommand(cmd)
}

func registerDraftList(parent *cobra.Command, globals *GlobalFlags) {
	tflags := &transcriptFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List drafts (unscheduled), including any started in-app; scheduled messages are under 'message scheduled list'",
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
			if wantsTranscript(globals) {
				return renderDraftsTranscript(ctx, globals, cc, tflags, drafts)
			}
			items := make([]any, len(drafts))
			for i, d := range drafts {
				items[i] = draftItem(d)
			}
			return printList(globals, items, nil)
		},
	}
	enableTranscript(cmd, tflags)
	registerResolveFlag(cmd, &tflags.resolve, resolveAuto)
	parent.AddCommand(cmd)
}

func registerDraftGet(parent *cobra.Command, globals *GlobalFlags) {
	tflags := &transcriptFlags{}
	cmd := &cobra.Command{
		Use:               "get <target|id>",
		Short:             "Show a draft by id, or by target when it has exactly one",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: draftArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, d, err := resolveDraftArg(cmd.Context(), globals, args[0])
			if err != nil {
				return err
			}
			if wantsTranscript(globals) {
				return renderDraftsTranscript(cmd.Context(), globals, cc, tflags, []slack.Draft{d})
			}
			return emitItem(globals, draftItem(d))
		},
	}
	enableTranscript(cmd, tflags)
	registerResolveFlag(cmd, &tflags.resolve, resolveAuto)
	parent.AddCommand(cmd)
}

func registerDraftEdit(parent *cobra.Command, globals *GlobalFlags) {
	var blocksPath string
	var slackMarkdown bool
	var forward string
	var attach []string
	var threadTS string
	cmd := &cobra.Command{
		Use:               "edit <target|id> [text]",
		Short:             "Replace a draft's content (by id, or by target when it has exactly one)",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: draftArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, d, err := resolveDraftArg(ctx, globals, args[0])
			if err != nil {
				return err
			}
			text := ""
			if len(args) > 1 {
				text = args[1]
			}
			// A replace rebuilds the draft from scratch, so keep its existing
			// thread unless --thread-ts overrides it (otherwise an edit would
			// silently demote a thread reply to a channel draft).
			if threadTS == "" {
				threadTS = d.ThreadTS
			}
			req, err := buildDraftContent(ctx, cmd, cc, d.ChannelID, render.TargetChannel, text, blocksPath, slackMarkdown, forward, attach, threadTS)
			if err != nil {
				return err
			}
			updated, err := slack.UpdateDraft(ctx, cc.Client, d.ID, req.outgoing(), 0)
			if err != nil {
				return err
			}
			return printSingle(globals, draftPayload(updated, "draft updated"))
		},
	}
	cmd.Flags().StringVar(&blocksPath, "blocks", "", "Path to a JSON file with Block Kit blocks ('-' = stdin)")
	cmd.Flags().BoolVar(&slackMarkdown, "slack-markdown", false, "Interpret text as Slack mrkdwn instead of standard Markdown")
	cmd.Flags().StringVar(&forward, "forward", "", "Forward a message: a Slack permalink whose message is embedded (text becomes an optional comment; same workspace only)")
	cmd.Flags().StringArrayVar(&attach, "attach", nil, "Attach a local file to the draft (repeatable)")
	cmd.Flags().StringVar(&threadTS, "thread-ts", "", "Reply in a thread: the thread root ts (defaults to the draft's current thread)")
	parent.AddCommand(cmd)
}

func registerDraftDelete(parent *cobra.Command, globals *GlobalFlags) {
	var yes bool
	cmd := &cobra.Command{
		Use:               "delete <target|id>",
		Short:             "Discard a draft by id, or by target when it has exactly one (destructive: requires --yes)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: draftArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, d, err := resolveDraftArg(ctx, globals, args[0])
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
	var sched scheduleFlags
	cmd := &cobra.Command{
		Use:               "send <target|id>",
		Short:             "Send a draft now (by id, or by target when it has exactly one), or --schedule/--schedule-in to promote it to a scheduled message",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: draftArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			postAt, err := sched.resolvePostAt(time.Now())
			if err != nil {
				return err
			}
			cc, d, err := resolveDraftArg(ctx, globals, args[0])
			if err != nil {
				return err
			}
			return runDraftSend(ctx, globals, cc, d, postAt)
		},
	}
	sched.register(cmd, "Promote to a scheduled message")
	parent.AddCommand(cmd)
}

// runDraftSend dispatches a draft send across its three modes: a non-zero
// postAt promotes the draft to a scheduled message in place; a draft with files
// posts natively via files.share; a fileless draft posts via chat.postMessage.
func runDraftSend(ctx context.Context, globals *GlobalFlags, cc *clientContext, d slack.Draft, postAt int64) error {
	msg := slack.OutgoingMessage{ChannelID: d.ChannelID, ThreadTS: d.ThreadTS, Blocks: d.Blocks, FileIDs: d.FileIDs}
	switch {
	case postAt != 0:
		// Promote the plain draft to a scheduled message in place (same id); no
		// separate post/delete — Slack delivers it (with its files) at post_at.
		// UpdateDraft re-sends file_ids, so attachments survive.
		promoted, err := slack.UpdateDraft(ctx, cc.Client, d.ID, msg, postAt)
		if err != nil {
			return err
		}
		// Prefer the time Slack echoes; fall back to the requested time when the
		// update response omits date_scheduled.
		scheduledAt := promoted.PostAt
		if scheduledAt == 0 {
			scheduledAt = postAt
		}
		payload := scheduleResultPayload(
			slack.ScheduleResult{ChannelID: promoted.ChannelID, ScheduledMessageID: promoted.ID, PostAt: scheduledAt}, promoted.ThreadTS)
		payload["note"] = "promoted the draft to a scheduled message — manage it under 'message scheduled'"
		return printSingle(globals, payload)

	case len(d.FileIDs) > 0:
		// A draft with attachments can't be re-posted via chat.postMessage (it
		// can't attach already-uploaded files), so send it natively with
		// files.share, which posts and removes the draft in one call.
		result, err := slack.ShareDraft(ctx, cc.Client, d)
		if err != nil {
			return err
		}
		return printSingle(globals, postedMessagePayload(result, cc.WorkspaceURL, d.ThreadTS))

	default:
		// A fileless draft posts via chat.postMessage; passing draft_id makes
		// Slack remove the draft as part of the post (native, atomic — no
		// separate delete to race or leave stale).
		msg.DraftID = d.ID
		result, err := slack.PostMessage(ctx, cc.Client, msg)
		if err != nil {
			return err
		}
		return printSingle(globals, postedMessagePayload(result, cc.WorkspaceURL, d.ThreadTS))
	}
}

// --- shared draft helpers ---------------------------------------------------

// buildDraftRequest parses the target and validates the text/--blocks into a
// sendRequest (reusing the send build path, minus scheduling). Any --attach
// files are uploaded to file ids and attached to the draft directly (drafts
// keep their rich_text blocks, so links/formatting survive — unlike a direct
// attachment send, which posts plain text).
func buildDraftRequest(ctx context.Context, cmd *cobra.Command, globals *GlobalFlags, args []string, blocksPath string, slackMarkdown bool, forward string, attach []string, threadTS string) (sendRequest, *clientContext, error) {
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
	req, err := buildDraftContent(ctx, cmd, cc, channelID, target.Kind, text, blocksPath, slackMarkdown, forward, attach, threadTS)
	if err != nil {
		return sendRequest{}, nil, err
	}
	if target.Kind == render.TargetURL {
		// A permalink target drafts a reply in that message's thread, mirroring
		// `message send` — the permalink is the natural "reply to this" handle.
		req.threadTS, err = threadRootTS(ctx, cc, target.Ref, false)
		if err != nil {
			return sendRequest{}, nil, err
		}
	}
	return req, cc, nil
}

// buildDraftContent turns text/--blocks/--forward/--attach into a sendRequest
// bound to channelID. It is the shared body of create (target → channel) and
// edit (resolved draft → channel): mentions resolve, attachments upload to file
// ids (drafts keep their rich_text blocks, so links/formatting survive — unlike
// a direct attachment send, which posts plain text).
func buildDraftContent(ctx context.Context, cmd *cobra.Command, cc *clientContext, channelID string, targetKind render.TargetKind, text, blocksPath string, slackMarkdown bool, forward string, attach []string, threadTS string) (sendRequest, error) {
	if forward != "" {
		var err error
		text, err = resolveForward(text, forward, cc.WorkspaceURL)
		if err != nil {
			return sendRequest{}, err
		}
	}
	text = slack.ResolveMentions(ctx, cc.Client, text)
	text = slack.ResolveChannelMentions(ctx, cc.Client, text)
	req, err := buildSendRequest(cmd.InOrStdin(), targetKind, text, sendFlags{blocksPath: blocksPath, slackMarkdown: slackMarkdown, forward: forward, attach: attach, threadTS: threadTS}, time.Now())
	if err != nil {
		return sendRequest{}, err
	}
	req.channelID = channelID
	if len(req.attachPaths) > 0 {
		ids, uerr := cc.Client.UploadDraftFiles(ctx, req.attachPaths)
		if uerr != nil {
			return sendRequest{}, uerr
		}
		req.fileIDs = ids
		req.attachPaths = nil
	}
	return req, nil
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

// resolveDraftArg resolves a draft from either a draft id (Dr…) or a target.
// A draft id addresses one draft directly. A target resolves to its draft only
// when it has exactly one — since drafts are many-per-target, more than one is
// ambiguous and we ask for an id rather than silently acting on the wrong one
// (e.g. a draft the user started in-app). Browser auth only.
func resolveDraftArg(ctx context.Context, globals *GlobalFlags, arg string) (*clientContext, slack.Draft, error) {
	if slack.IsDraftID(arg) {
		cc, err := getClient(globals)
		if err != nil {
			return nil, slack.Draft{}, err
		}
		if err := requireDraftAuth(cc); err != nil {
			return nil, slack.Draft{}, err
		}
		d, ok, err := slack.DraftByID(ctx, cc.Client, arg)
		if err != nil {
			return nil, slack.Draft{}, err
		}
		if !ok {
			return nil, slack.Draft{}, agenterrors.Newf(agenterrors.FixableByAgent, "no draft with id %s", arg).
				WithHint("list drafts with 'message draft list'")
		}
		return cc, d, nil
	}

	target, err := render.ParseTarget(arg)
	if err != nil {
		return nil, slack.Draft{}, err
	}
	cc, channelID, err := resolveDraftClient(ctx, globals, target)
	if err != nil {
		return nil, slack.Draft{}, err
	}
	drafts, err := slack.DraftsForChannel(ctx, cc.Client, channelID)
	if err != nil {
		return nil, slack.Draft{}, err
	}
	switch len(drafts) {
	case 0:
		return nil, slack.Draft{}, agenterrors.Newf(agenterrors.FixableByAgent, "no draft for %s", arg).
			WithHint("create one with 'message draft create " + arg + " …'; scheduled messages are under 'message scheduled list'")
	case 1:
		return cc, drafts[0], nil
	default:
		ids := make([]string, len(drafts))
		for i, d := range drafts {
			ids[i] = d.ID
		}
		return nil, slack.Draft{}, agenterrors.Newf(agenterrors.FixableByAgent, "%d drafts for %s: %s", len(drafts), arg, strings.Join(ids, ", ")).
			WithHint("pass a draft id (Dr…) instead of a target")
	}
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
	if d.ThreadTS != "" {
		item["thread_ts"] = d.ThreadTS
	}
	if d.PostAt > 0 {
		item["post_at"] = d.PostAt
	}
	// file_ids ride along on the same drafts.list response toDraft already
	// parsed — surfacing them here costs no extra call.
	if len(d.FileIDs) > 0 {
		item["file_ids"] = d.FileIDs
	}
	return item
}

func draftPayload(d slack.Draft, note string) map[string]any {
	payload := map[string]any{"ok": true, "draft_id": d.ID, "channel_id": d.ChannelID, "note": note}
	if d.ThreadTS != "" {
		payload["thread_ts"] = d.ThreadTS
	}
	return payload
}
