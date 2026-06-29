package cli

import (
	"context"
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
	resolve          string // --resolve: none | cached | auto | fresh
	slackMarkdown    bool
}

func (f *readFlags) register(cmd *cobra.Command, defaultMaxBody int) {
	cmd.Flags().StringVar(&f.ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
	cmd.Flags().StringVar(&f.threadTS, "thread-ts", "", "Thread root ts hint")
	cmd.Flags().IntVar(&f.maxBodyChars, "max-body-chars", defaultMaxBody, "Max content chars per message (-1 = unlimited)")
	cmd.Flags().BoolVar(&f.includeReactions, "include-reactions", false, "Include reactions and reacting users")
	registerResolveFlag(cmd, &f.resolve, resolveAuto)
	cmd.Flags().BoolVar(&f.slackMarkdown, "slack-markdown", false, "Render content as Slack mrkdwn instead of standard Markdown")
}

// resolveMode returns the parsed --resolve value; callers validate(f) first so
// the parse here cannot fail.
func (f *readFlags) resolveMode() resolveMode { return resolveMode(f.resolve) }

func (f *readFlags) validate() error {
	_, err := parseResolveMode(f.resolve)
	return err
}

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

// outboundTextAndBlocks renders authored text into the (text, blocks) pair an
// outbound message carries: the mrkdwn-escaped fallback text and the rich_text
// blocks, nil when the text is plain so callers omit the blocks field. Mentions
// must already be resolved. workspaceURL (the sending workspace) lets a
// same-workspace message permalink render as an inline chip; pass "" to skip.
func outboundTextAndBlocks(text string, slackMarkdown bool, workspaceURL string) (string, []any) {
	rtBlocks, outboundText := render.RenderOutbound(text, slackMarkdown)
	rtBlocks = render.UpgradeMessageMentions(rtBlocks, text, slackMarkdown, workspaceURL)
	var blocks []any
	if len(rtBlocks) > 0 {
		blocks = toAnySlice(rtBlocks)
	}
	return render.FormatOutboundText(outboundText), blocks
}

// warnTruncatedURL nudges about shell-eaten permalinks (thread_ts without cid).
func warnTruncatedURL(globals *GlobalFlags, ref *render.MessageRef) {
	if ref.PossiblyTruncated {
		emitNotice(globals, "the URL looks truncated (thread_ts without cid)",
			"quote Slack URLs so the shell doesn't eat '&'")
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

// resolveReferencedEntities expands the users, channels, and usergroups a set of
// messages references into referenced_* output maps, per --resolve. Returns nil
// when resolution is off or nothing resolved; the caller merges the entries into
// its payload/meta.
func resolveReferencedEntities(ctx context.Context, cc *clientContext, globals *GlobalFlags, flags *readFlags, messages []render.MessageSummary) map[string]any {
	mode := flags.resolveMode()
	if !mode.resolve() {
		return nil
	}
	refs := render.CollectReferencedIDs(messages, flags.includeReactions)
	ents := slack.ResolveReferenced(ctx, cc.Client, refs, mode.policy())
	maybeWarmHint(globals, mode, ents.Fetched)
	out := referencedPayload(ents)
	if len(out) == 0 {
		return nil
	}
	return out
}

// referencedPayload spreads resolved referenced entities into the output keys.
func referencedPayload(ents slack.ReferencedEntities) map[string]any {
	out := map[string]any{}
	if ents.Users != nil {
		out["referenced_users"] = ents.Users
	}
	if ents.Channels != nil {
		out["referenced_channels"] = ents.Channels
	}
	if ents.Usergroups != nil {
		out["referenced_usergroups"] = ents.Usergroups
	}
	return out
}

// maybeWarmHint nudges toward `cache warm` when --resolve auto had to fetch
// uncached ids (cached/fresh/none are explicit choices, so no hint there). It
// names the specific categories that missed, so the hint is an exact command —
// not a claim that the whole cache was cold (most ids may have been cache hits).
func maybeWarmHint(globals *GlobalFlags, mode resolveMode, fetched []string) {
	if mode != resolveAuto || len(fetched) == 0 {
		return
	}
	emitNotice(globals, "--resolve fetched uncached "+strings.Join(fetched, ", ")+" via API",
		"run 'cache warm "+strings.Join(fetched, " ")+"' to resolve these from cache next time")
}
