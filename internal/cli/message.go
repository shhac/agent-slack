package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/htmlmd"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerMessage(parent *cobra.Command, globals *GlobalFlags) {
	messageCmd := &cobra.Command{
		Use:   "message",
		Short: "Read and write Slack messages (token-efficient JSON)",
	}
	parent.AddCommand(messageCmd)
	handleUnknownSubcommand(messageCmd)

	registerMessageGet(messageCmd, globals)
	registerMessageList(messageCmd, globals)
	registerMessageSend(messageCmd, globals)
	registerMessageDraft(messageCmd, globals)
	registerMessageEdit(messageCmd, globals)
	registerMessageDelete(messageCmd, globals)
	registerMessageReact(messageCmd, globals)
	registerMessageScheduled(messageCmd, globals)
}

// readFlags are the shared read-path options.
type readFlags struct {
	ts               string
	threadTS         string
	maxBodyChars     int
	includeReactions bool
	resolveUsers     bool
	refreshUsers     bool
	slackMarkdown    bool
}

func (f *readFlags) register(cmd *cobra.Command, defaultMaxBody int) {
	cmd.Flags().StringVar(&f.ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
	cmd.Flags().StringVar(&f.threadTS, "thread-ts", "", "Thread root ts hint")
	cmd.Flags().IntVar(&f.maxBodyChars, "max-body-chars", defaultMaxBody, "Max content chars per message (-1 = unlimited)")
	cmd.Flags().BoolVar(&f.includeReactions, "include-reactions", false, "Include reactions and reacting users")
	cmd.Flags().BoolVar(&f.resolveUsers, "resolve-users", false, "Resolve referenced user IDs to profiles")
	cmd.Flags().BoolVar(&f.refreshUsers, "refresh-users", false, "Refresh the user cache before resolving (implies --resolve-users)")
	cmd.Flags().BoolVar(&f.slackMarkdown, "slack-markdown", false, "Render content as Slack mrkdwn instead of standard Markdown")
}

func (f *readFlags) shouldResolveUsers() bool { return f.resolveUsers || f.refreshUsers }

// scheduleFlags is the shared --schedule/--schedule-in pair. verb tailors the
// help text ("Schedule" for send, "Promote to a scheduled message" for draft).
type scheduleFlags struct {
	schedule   string
	scheduleIn string
}

func (f *scheduleFlags) register(cmd *cobra.Command, verb string) {
	cmd.Flags().StringVar(&f.schedule, "schedule", "", verb+" at an ISO 8601 time with timezone, or a unix timestamp")
	cmd.Flags().StringVar(&f.scheduleIn, "schedule-in", "", verb+" after a duration (30m, 2d, tomorrow 9am, monday 9am)")
}

func (f scheduleFlags) resolvePostAt(now time.Time) (int64, error) {
	return slack.ResolveSchedulePostAt(f.schedule, f.scheduleIn, now)
}

// warnTruncatedURL nudges about shell-eaten permalinks (thread_ts without cid).
func warnTruncatedURL(globals *GlobalFlags, ref *render.MessageRef) {
	if ref.PossiblyTruncated {
		_, _ = fmt.Fprintln(globals.stderr,
			"Warning: the URL looks truncated (thread_ts without cid) — quote Slack URLs to stop the shell eating '&'")
	}
}

// resolveTargetClient maps a parsed CLI <target> to a connected client and a
// channel ID — the kernel every target-taking command shares. Permalinks pin
// their workspace (overriding --workspace); channels resolve names to IDs.
// rejectUserTargetMsg is the per-command error for U… targets (preserved for
// output parity); empty means "open the DM and use it as the channel".
func resolveTargetClient(ctx context.Context, globals *GlobalFlags, target render.Target, rejectUserTargetMsg string) (*clientContext, string, error) {
	switch target.Kind {
	case render.TargetURL:
		cc, err := getClientForWorkspace(globals, target.Ref.WorkspaceURL)
		return cc, target.Ref.ChannelID, err
	case render.TargetUser:
		if rejectUserTargetMsg != "" {
			return nil, "", agenterrors.New(rejectUserTargetMsg, agenterrors.FixableByAgent).
				WithHint("use a channel name, channel ID, or message URL")
		}
		cc, err := getClient(globals)
		if err != nil {
			return nil, "", err
		}
		// target.UserID may be a U… id or an "@handle"; resolve either to an id.
		userID, err := slack.ResolveUserID(ctx, cc.Client, target.UserID)
		if err != nil {
			return nil, "", err
		}
		channelID, err := slack.OpenDMChannel(ctx, cc.Client, userID)
		return cc, channelID, err
	default:
		// A channel URL pins its workspace like a permalink; bare names/IDs
		// pass "" and fall back to --workspace/default.
		cc, err := getClientForWorkspace(globals, target.WorkspaceURL)
		if err != nil {
			return nil, "", err
		}
		channelID, err := slack.ResolveChannelID(ctx, cc.Client, target.Channel)
		return cc, channelID, err
	}
}

// resolveMessageTarget turns a CLI <target> (+ --ts/--thread-ts) into a
// connected client and a concrete message ref. Permalinks pin the workspace.
func resolveMessageTarget(ctx context.Context, globals *GlobalFlags, targetInput, ts, threadTS string) (*clientContext, *render.MessageRef, error) {
	target, err := render.ParseTarget(targetInput)
	if err != nil {
		return nil, nil, err
	}
	if target.Kind == render.TargetChannel && strings.TrimSpace(ts) == "" {
		return nil, nil, agenterrors.New(`when targeting a channel, you must pass --ts "<seconds>.<micros>"`, agenterrors.FixableByAgent).
			WithHint("'agent-slack message list <channel>' shows recent ts values")
	}
	if target.Kind == render.TargetURL {
		warnTruncatedURL(globals, target.Ref)
	}
	cc, channelID, err := resolveTargetClient(ctx, globals, target, "this command does not support user ID targets")
	if err != nil {
		return nil, nil, err
	}
	if target.Kind == render.TargetURL {
		return cc, target.Ref, nil
	}
	return cc, &render.MessageRef{
		WorkspaceURL: cc.WorkspaceURL,
		ChannelID:    channelID,
		MessageTS:    strings.TrimSpace(ts),
		ThreadTSHint: strings.TrimSpace(threadTS),
		Raw:          targetInput,
	}, nil
}

// threadRootTS fetches the message a ref points at and returns its thread
// root (the message's own ts when it isn't a reply).
func threadRootTS(ctx context.Context, cc *clientContext, ref *render.MessageRef, includeReactions bool) (string, error) {
	msg, err := slack.FetchMessage(ctx, cc.Client, ref, includeReactions)
	if err != nil {
		return "", err
	}
	if msg.ThreadTS != "" {
		return msg.ThreadTS, nil
	}
	return msg.TS, nil
}

func messageDownloadOptions(globals *GlobalFlags) slack.MessageDownloads {
	return slack.MessageDownloads{
		DestDir:        downloadsDir(),
		CanvasMarkdown: htmlmd.Convert,
		Warn:           globals.stderr,
	}
}

func resolveReferencedUsers(ctx context.Context, cc *clientContext, flags *readFlags, messages []render.MessageSummary) map[string]slack.CompactUser {
	if !flags.shouldResolveUsers() {
		return nil
	}
	ids := render.CollectReferencedUserIDs(messages, flags.includeReactions)
	users := slack.ResolveUsersByID(ctx, cc.Client, ids, flags.refreshUsers)
	return slack.ToReferencedUsers(ids, users)
}
