package cli

import (
	"context"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
	"strings"
)

func registerMessageGet(parent *cobra.Command, globals *GlobalFlags) {
	flags := &readFlags{}
	noDownload := false
	cmd := &cobra.Command{
		Use:   "get <target>",
		Short: "Fetch one message (with thread summary); files download to the cache dir",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, ref, err := resolveMessageTarget(ctx, globals, args[0], flags.ts, flags.threadTS)
			if err != nil {
				return err
			}
			msg, err := slack.FetchMessage(ctx, cc.Client, ref, flags.includeReactions)
			if err != nil {
				return err
			}
			thread, err := slack.ThreadSummary(ctx, cc.Client, ref.ChannelID, msg)
			if err != nil {
				return err
			}
			downloads := map[string]render.DownloadResult{}
			if !noDownload {
				downloads = slack.DownloadMessageFiles(ctx, cc.Client, []render.MessageSummary{msg}, messageDownloadOptions(globals))
			}
			compact := render.ToCompactMessage(msg, render.CompactOptions{
				MaxBodyChars:     flags.maxBodyChars,
				IncludeReactions: flags.includeReactions,
				DownloadedPaths:  downloads,
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
			if users := resolveReferencedUsers(ctx, cc, flags, []render.MessageSummary{msg}); users != nil {
				payload["referenced_users"] = users
			}
			return printSingle(globals, payload)
		},
	}
	flags.register(cmd, render.DefaultMaxBodyChars)
	cmd.Flags().BoolVar(&noDownload, "no-download", false, "Skip downloading attached files")
	parent.AddCommand(cmd)
}

func registerMessageList(parent *cobra.Command, globals *GlobalFlags) {
	flags := &readFlags{}
	var limit int
	var oldest, latest string
	var withReaction, withoutReaction []string
	var download bool
	cmd := &cobra.Command{
		Use:   "list <target>",
		Short: "List recent channel messages, or a full thread (--thread-ts/--ts or a thread permalink)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			target, err := render.ParseTarget(args[0])
			if err != nil {
				return err
			}
			if target.Kind == render.TargetUser {
				return agenterrors.New("message list does not support user ID targets", agenterrors.FixableByAgent).
					WithHint("use a channel name, channel ID, or message URL")
			}

			withReactions, err := normalizeReactionNames(withReaction)
			if err != nil {
				return err
			}
			withoutReactions, err := normalizeReactionNames(withoutReaction)
			if err != nil {
				return err
			}
			hasReactionFilters := len(withReactions) > 0 || len(withoutReactions) > 0

			// Permalink target → list that message's whole thread.
			if target.Kind == render.TargetURL {
				if hasReactionFilters {
					return agenterrors.New("reaction filters are only supported for channel history mode", agenterrors.FixableByAgent)
				}
				warnTruncatedURL(globals, target.Ref)
				cc, err := getClientForWorkspace(globals, target.Ref.WorkspaceURL)
				if err != nil {
					return err
				}
				rootTS, err := threadRootTS(ctx, cc, target.Ref, flags.includeReactions)
				if err != nil {
					return err
				}
				return printThread(ctx, globals, cc, flags, target.Ref.ChannelID, rootTS, download)
			}

			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			channelID, err := slack.ResolveChannelID(ctx, cc.Client, target.Channel)
			if err != nil {
				return err
			}

			threadTS := strings.TrimSpace(flags.threadTS)
			ts := strings.TrimSpace(flags.ts)

			// No thread specifier → recent channel history.
			if threadTS == "" && ts == "" {
				if hasReactionFilters && strings.TrimSpace(oldest) == "" {
					return agenterrors.New(`reaction filters require --oldest "<seconds>.<micros>" to bound the scan`, agenterrors.FixableByAgent).
						WithHint(`example: --with-reaction eyes --oldest "1770165109.628379"`)
				}
				messages, err := slack.FetchChannelHistory(ctx, cc.Client, slack.HistoryOptions{
					ChannelID:        channelID,
					Limit:            limit,
					Latest:           strings.TrimSpace(latest),
					Oldest:           strings.TrimSpace(oldest),
					IncludeReactions: flags.includeReactions || hasReactionFilters,
					WithReactions:    withReactions,
					WithoutReactions: withoutReactions,
				})
				if err != nil {
					return err
				}
				return printMessages(ctx, globals, cc, flags, messages, download, map[string]any{"channel_id": channelID}, false)
			}

			if hasReactionFilters {
				return agenterrors.New("reaction filters are only supported for channel history mode (without --thread-ts/--ts)", agenterrors.FixableByAgent)
			}

			rootTS := threadTS
			if rootTS == "" {
				ref := &render.MessageRef{WorkspaceURL: cc.WorkspaceURL, ChannelID: channelID, MessageTS: ts, Raw: args[0]}
				rootTS, err = threadRootTS(ctx, cc, ref, flags.includeReactions)
				if err != nil {
					return err
				}
			}
			return printThread(ctx, globals, cc, flags, channelID, rootTS, download)
		},
	}
	flags.register(cmd, render.DefaultMaxBodyChars)
	cmd.Flags().IntVar(&limit, "limit", 25, "Max messages (channel history mode, max 200)")
	cmd.Flags().StringVar(&oldest, "oldest", "", "Only messages after this ts")
	cmd.Flags().StringVar(&latest, "latest", "", "Only messages before this ts")
	cmd.Flags().StringArrayVar(&withReaction, "with-reaction", nil, "Only messages with this reaction (repeatable; requires --oldest)")
	cmd.Flags().StringArrayVar(&withoutReaction, "without-reaction", nil, "Only messages without this reaction (repeatable; requires --oldest)")
	cmd.Flags().BoolVar(&download, "download", false, "Download attached files to the cache dir")
	parent.AddCommand(cmd)
}

func printThread(ctx context.Context, globals *GlobalFlags, cc *clientContext, flags *readFlags, channelID, rootTS string, download bool) error {
	messages, err := slack.FetchThread(ctx, cc.Client, channelID, rootTS, flags.includeReactions)
	if err != nil {
		return err
	}
	meta := map[string]any{"channel_id": channelID, "thread_ts": rootTS}
	return printMessages(ctx, globals, cc, flags, messages, download, meta, true)
}

func printMessages(ctx context.Context, globals *GlobalFlags, cc *clientContext, flags *readFlags, messages []render.MessageSummary, download bool, meta map[string]any, threadMode bool) error {
	downloads := map[string]render.DownloadResult{}
	if download {
		downloads = slack.DownloadMessageFiles(ctx, cc.Client, messages, messageDownloadOptions(globals))
	}
	items := make([]any, 0, len(messages))
	for _, m := range messages {
		compact := render.ToCompactMessage(m, render.CompactOptions{
			MaxBodyChars:     flags.maxBodyChars,
			IncludeReactions: flags.includeReactions,
			DownloadedPaths:  downloads,
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
	if users := resolveReferencedUsers(ctx, cc, flags, messages); users != nil {
		meta["referenced_users"] = users
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
