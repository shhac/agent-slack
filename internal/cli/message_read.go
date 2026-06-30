package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerMessageGet(parent *cobra.Command, globals *GlobalFlags) {
	flags := &readFlags{}
	tflags := &transcriptFlags{}
	noDownload := false
	cmd := &cobra.Command{
		Use:               "get <target>",
		Short:             "Fetch one message (with thread summary); files download to the cache dir",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := flags.validate(); err != nil {
				return err
			}
			cc, ref, err := resolveMessageTarget(ctx, globals, args[0], flags.ts, flags.threadTS)
			if err != nil {
				return err
			}
			msg, err := slack.FetchMessage(ctx, cc.Client, ref, flags.includeReactions)
			if err != nil {
				return err
			}
			if wantsTranscript(globals) {
				if err := printTranscript(ctx, globals, cc, tflags, flags, []render.MessageSummary{msg}, false); err != nil {
					return err
				}
				return writeMessageGetFooter(ctx, globals, cc, ref, msg)
			}
			thread, err := slack.ThreadSummary(ctx, cc.Client, ref.ChannelID, msg)
			if err != nil {
				return err
			}
			downloads := map[string]render.DownloadResult{}
			if !noDownload {
				downloads = slack.DownloadMessageFiles(ctx, cc.Client, []render.MessageSummary{msg}, messageDownloadOptions(globals, cc))
			}
			compact := render.ToCompactMessage(msg, render.CompactOptions{
				MaxBodyChars:     flags.maxBodyChars,
				IncludeReactions: flags.includeReactions,
				DownloadedPaths:  downloads,
				SlackMarkdown:    flags.slackMarkdown,
			})
			payload := map[string]any{
				"message": compact,
				"permalink": render.BuildMessageURL(render.MessageURLParts{
					WorkspaceURL: ref.WorkspaceURL,
					ChannelID:    ref.ChannelID,
					MessageTS:    compact.TS,
					ThreadTS:     compact.ThreadTS,
				}),
			}
			if thread != nil {
				payload["thread"] = thread
			}
			for k, v := range resolveReferencedEntities(ctx, cc, globals, flags, []render.MessageSummary{msg}) {
				payload[k] = v
			}
			return emitItem(globals, payload)
		},
	}
	flags.register(cmd, render.DefaultMaxBodyChars)
	enableTranscript(cmd, tflags)
	cmd.Flags().BoolVar(&noDownload, "no-download", false, "Skip downloading attached files")
	parent.AddCommand(cmd)
}

func registerMessageList(parent *cobra.Command, globals *GlobalFlags) {
	flags := &readFlags{}
	tflags := &transcriptFlags{}
	var limit int
	var oldest, latest string
	var withReaction, withoutReaction []string
	var download bool
	cmd := &cobra.Command{
		Use:               "list <target>",
		Short:             "List recent channel messages, or a full thread (--thread-ts/--ts or a thread permalink)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := flags.validate(); err != nil {
				return err
			}
			target, err := render.ParseTarget(args[0])
			if err != nil {
				return err
			}
			ts := strings.TrimSpace(flags.ts)
			threadTS := strings.TrimSpace(flags.threadTS)
			plan, err := planMessageList(target.Kind, ts, threadTS, strings.TrimSpace(oldest), withReaction, withoutReaction)
			if err != nil {
				return err
			}

			switch plan.mode {
			case listModeURLThread:
				return listURLThread(ctx, globals, flags, tflags, target.Ref, download)
			case listModeHistory:
				opts := slack.HistoryOptions{
					Limit:            limit,
					Latest:           strings.TrimSpace(latest),
					Oldest:           strings.TrimSpace(oldest),
					IncludeReactions: flags.includeReactions || plan.hasReactionFilters,
					WithReactions:    plan.withReactions,
					WithoutReactions: plan.withoutReactions,
				}
				return listChannelHistory(ctx, globals, flags, tflags, target, opts, download)
			default: // listModeThread
				return listChannelThread(ctx, globals, flags, tflags, target, ts, threadTS, args[0], download)
			}
		},
	}
	flags.register(cmd, render.DefaultMaxBodyChars)
	enableTranscript(cmd, tflags)
	cmd.Flags().IntVar(&limit, "limit", 25, "Max messages (channel history mode, max 200)")
	cmd.Flags().StringVar(&oldest, "oldest", "", "Only messages after this ts")
	cmd.Flags().StringVar(&latest, "latest", "", "Only messages before this ts")
	cmd.Flags().StringArrayVar(&withReaction, "with-reaction", nil, "Only messages with this reaction (repeatable; requires --oldest)")
	cmd.Flags().StringArrayVar(&withoutReaction, "without-reaction", nil, "Only messages without this reaction (repeatable; requires --oldest)")
	cmd.Flags().BoolVar(&download, "download", false, "Download attached files to the cache dir")
	parent.AddCommand(cmd)
}

// listURLThread lists the whole thread a permalink points into.
func listURLThread(ctx context.Context, globals *GlobalFlags, flags *readFlags, tflags *transcriptFlags, ref *render.MessageRef, download bool) error {
	warnTruncatedURL(globals, ref)
	cc, err := getClientForWorkspace(globals, ref.WorkspaceURL)
	if err != nil {
		return err
	}
	rootTS, err := threadRootTS(ctx, cc, ref, flags.includeReactions)
	if err != nil {
		return err
	}
	return printThread(ctx, globals, cc, flags, tflags, ref.ChannelID, rootTS, download)
}

// listChannelHistory lists recent channel messages, with optional reaction
// filters. opts.ChannelID is filled in after target resolution.
func listChannelHistory(ctx context.Context, globals *GlobalFlags, flags *readFlags, tflags *transcriptFlags, target render.Target, opts slack.HistoryOptions, download bool) error {
	cc, channelID, err := resolveTargetClient(ctx, globals, target, "")
	if err != nil {
		return err
	}
	opts.ChannelID = channelID
	messages, err := slack.FetchChannelHistory(ctx, cc.Client, opts)
	if err != nil {
		return err
	}
	return printMessages(ctx, globals, cc, flags, tflags, messages, download, map[string]any{"channel_id": channelID}, false)
}

// listChannelThread lists the thread named by --thread-ts, or the thread
// containing the --ts message when only --ts was given.
func listChannelThread(ctx context.Context, globals *GlobalFlags, flags *readFlags, tflags *transcriptFlags, target render.Target, ts, threadTS, rawTarget string, download bool) error {
	cc, channelID, err := resolveTargetClient(ctx, globals, target, "")
	if err != nil {
		return err
	}
	rootTS := threadTS
	if rootTS == "" {
		ref := &render.MessageRef{WorkspaceURL: cc.WorkspaceURL, ChannelID: channelID, MessageTS: ts, Raw: rawTarget}
		rootTS, err = threadRootTS(ctx, cc, ref, flags.includeReactions)
		if err != nil {
			return err
		}
	}
	return printThread(ctx, globals, cc, flags, tflags, channelID, rootTS, download)
}

// listMode is which of message list's three behaviors an invocation gets.
type listMode int

const (
	listModeURLThread listMode = iota // permalink target → that message's thread
	listModeHistory                   // channel target, no thread specifier
	listModeThread                    // channel target + --ts/--thread-ts
)

// listPlan is the resolved shape of a `message list` invocation: the mode plus
// the normalized reaction filters that feed history mode.
type listPlan struct {
	mode               listMode
	withReactions      []string
	withoutReactions   []string
	hasReactionFilters bool
}

// planMessageList normalizes the reaction-name filters and classifies the list
// mode — the pure validation the RunE closure used to inline, now testable
// without a live command.
func planMessageList(targetKind render.TargetKind, ts, threadTS, oldest string, withReaction, withoutReaction []string) (listPlan, error) {
	withReactions, err := normalizeReactionNames(withReaction)
	if err != nil {
		return listPlan{}, err
	}
	withoutReactions, err := normalizeReactionNames(withoutReaction)
	if err != nil {
		return listPlan{}, err
	}
	hasReactionFilters := len(withReactions) > 0 || len(withoutReactions) > 0
	mode, err := resolveListMode(targetKind, ts, threadTS, oldest, hasReactionFilters)
	if err != nil {
		return listPlan{}, err
	}
	return listPlan{mode, withReactions, withoutReactions, hasReactionFilters}, nil
}

// resolveListMode classifies the invocation and enforces the reaction-filter
// rules, keeping each mode's original error wording. User targets (a U… id or
// "@handle") classify exactly like channels — history by default, thread with
// --ts/--thread-ts — and the DM is opened during target resolution.
func resolveListMode(targetKind render.TargetKind, ts, threadTS, oldest string, hasReactionFilters bool) (listMode, error) {
	if targetKind == render.TargetURL {
		if hasReactionFilters {
			return 0, agenterrors.New("reaction filters are only supported for channel history mode", agenterrors.FixableByAgent)
		}
		return listModeURLThread, nil
	}
	if ts == "" && threadTS == "" {
		if hasReactionFilters && oldest == "" {
			return 0, agenterrors.New(`reaction filters require --oldest "<seconds>.<micros>" to bound the scan`, agenterrors.FixableByAgent).
				WithHint(`example: --with-reaction eyes --oldest "1770165109.628379"`)
		}
		return listModeHistory, nil
	}
	if hasReactionFilters {
		return 0, agenterrors.New("reaction filters are only supported for channel history mode (without --thread-ts/--ts)", agenterrors.FixableByAgent)
	}
	return listModeThread, nil
}

func printThread(ctx context.Context, globals *GlobalFlags, cc *clientContext, flags *readFlags, tflags *transcriptFlags, channelID, rootTS string, download bool) error {
	messages, err := slack.FetchThread(ctx, cc.Client, channelID, rootTS, flags.includeReactions)
	if err != nil {
		return err
	}
	meta := map[string]any{"channel_id": channelID, "thread_ts": rootTS}
	return printMessages(ctx, globals, cc, flags, tflags, messages, download, meta, true)
}

func printMessages(ctx context.Context, globals *GlobalFlags, cc *clientContext, flags *readFlags, tflags *transcriptFlags, messages []render.MessageSummary, download bool, meta map[string]any, threadMode bool) error {
	if wantsTranscript(globals) {
		return printTranscript(ctx, globals, cc, tflags, flags, messages, threadMode)
	}
	downloads := map[string]render.DownloadResult{}
	if download {
		downloads = slack.DownloadMessageFiles(ctx, cc.Client, messages, messageDownloadOptions(globals, cc))
	}
	items := make([]any, 0, len(messages))
	for _, m := range messages {
		compact := render.ToCompactMessage(m, render.CompactOptions{
			MaxBodyChars:     flags.maxBodyChars,
			IncludeReactions: flags.includeReactions,
			DownloadedPaths:  downloads,
			SlackMarkdown:    flags.slackMarkdown,
		})
		if threadMode {
			// Redundant in thread output: every row shares the meta line's
			// channel_id/thread_ts.
			compact.ChannelID = ""
			compact.ThreadTS = ""
		}
		items = append(items, compact)
	}
	if meta == nil {
		meta = map[string]any{}
	}
	for k, v := range resolveReferencedEntities(ctx, cc, globals, flags, messages) {
		meta[k] = v
	}
	if len(meta) == 0 {
		meta = nil
	}
	return printList(globals, items, meta)
}

func normalizeReactionNames(raw []string) ([]string, error) {
	normalized := make([]string, 0, len(raw))
	for _, value := range raw {
		name, err := render.NormalizeReactionName(value)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, name)
	}
	return dedupeStrings(normalized), nil
}
