package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/htmlmd"
	"github.com/shhac/agent-slack/internal/output"
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
}

func (f *readFlags) register(cmd *cobra.Command, defaultMaxBody int) {
	cmd.Flags().StringVar(&f.ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
	cmd.Flags().StringVar(&f.threadTS, "thread-ts", "", "Thread root ts hint")
	cmd.Flags().IntVar(&f.maxBodyChars, "max-body-chars", defaultMaxBody, "Max content chars per message (-1 = unlimited)")
	cmd.Flags().BoolVar(&f.includeReactions, "include-reactions", false, "Include reactions and reacting users")
	cmd.Flags().BoolVar(&f.resolveUsers, "resolve-users", false, "Resolve referenced user IDs to profiles")
	cmd.Flags().BoolVar(&f.refreshUsers, "refresh-users", false, "Refresh the user cache before resolving (implies --resolve-users)")
}

func (f *readFlags) shouldResolveUsers() bool { return f.resolveUsers || f.refreshUsers }

// warnTruncatedURL nudges about shell-eaten permalinks (thread_ts without cid).
func warnTruncatedURL(ref *render.MessageRef) {
	if ref.PossiblyTruncated {
		_, _ = fmt.Fprintln(output.Stderr(),
			"Warning: the URL looks truncated (thread_ts without cid) — quote Slack URLs to stop the shell eating '&'")
	}
}

// userTargetPolicy says what a U… target means to a command: an error, or
// "open the DM and use it as the channel".
type userTargetPolicy int

const (
	rejectUserTargets userTargetPolicy = iota
	openDMForUserTargets
)

// resolveTargetClient maps a parsed CLI <target> to a connected client and a
// channel ID — the kernel every target-taking command shares. Permalinks pin
// their workspace (overriding --workspace); channels resolve names to IDs;
// user targets follow the caller's policy (rejectMsg is the per-command
// error wording, preserved for output parity).
func resolveTargetClient(ctx context.Context, globals *GlobalFlags, target render.Target, policy userTargetPolicy, rejectMsg string) (*clientContext, string, error) {
	switch target.Kind {
	case render.TargetURL:
		cc, err := getClientForWorkspace(globals, target.Ref.WorkspaceURL)
		return cc, target.Ref.ChannelID, err
	case render.TargetUser:
		if policy == rejectUserTargets {
			return nil, "", agenterrors.New(rejectMsg, agenterrors.FixableByAgent).
				WithHint("use a channel name, channel ID, or message URL")
		}
		cc, err := getClient(globals)
		if err != nil {
			return nil, "", err
		}
		channelID, err := slack.OpenDMChannel(ctx, cc.Client, target.UserID)
		return cc, channelID, err
	default:
		cc, err := getClient(globals)
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
		warnTruncatedURL(target.Ref)
	}
	cc, channelID, err := resolveTargetClient(ctx, globals, target, rejectUserTargets, "this command does not support user ID targets")
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

func messageDownloadOptions() slack.MessageDownloads {
	return slack.MessageDownloads{
		DestDir:        downloadsDir(),
		CanvasMarkdown: htmlmd.Convert,
		Warn:           output.Stderr(),
	}
}

func resolveReferencedUsers(ctx context.Context, cc *clientContext, flags *readFlags, messages []render.MessageSummary) map[string]slack.CompactUser {
	if !flags.shouldResolveUsers() {
		return nil
	}
	ids := render.CollectReferencedUserIDs(messages, flags.includeReactions)
	users := slack.ResolveUsersByID(ctx, cc.Client, cc.WorkspaceURL, ids, slack.ResolveUsersOptions{
		CacheDir:     appCacheDir(),
		ForceRefresh: flags.refreshUsers,
	})
	return slack.ToReferencedUsers(ids, users)
}

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
				downloads = slack.DownloadMessageFiles(ctx, cc.Client, []render.MessageSummary{msg}, messageDownloadOptions())
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
				warnTruncatedURL(target.Ref)
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
		downloads = slack.DownloadMessageFiles(ctx, cc.Client, messages, messageDownloadOptions())
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
	var out []string
	seen := map[string]bool{}
	for _, value := range raw {
		name, err := render.NormalizeReactionName(value)
		if err != nil {
			return nil, err
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

func registerMessageSend(parent *cobra.Command, globals *GlobalFlags) {
	var threadTS, blocksPath, schedule, scheduleIn string
	var attach []string
	var replyBroadcast bool
	cmd := &cobra.Command{
		Use:   "send <target> [text]",
		Short: "Send or schedule a message (channel, #name, U…/DM, or permalink to reply in-thread)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			text := ""
			if len(args) > 1 {
				text = args[1]
			}
			if text == "" && len(attach) == 0 && blocksPath == "" {
				return agenterrors.New("message text is required unless --attach or --blocks is provided", agenterrors.FixableByAgent)
			}

			postAt, err := slack.ResolveSchedulePostAt(schedule, scheduleIn, time.Now())
			if err != nil {
				return err
			}
			attachPaths := dedupeStrings(attach)
			if postAt != 0 && len(attachPaths) > 0 {
				return agenterrors.New("--schedule/--schedule-in cannot be combined with --attach (scheduled messages do not support uploads)", agenterrors.FixableByAgent)
			}
			if blocksPath != "" && len(attachPaths) > 0 {
				return agenterrors.New("--blocks cannot be combined with --attach", agenterrors.FixableByAgent)
			}

			formatted := render.FormatOutboundText(text)
			var blocks []any
			if blocksPath != "" {
				blocks, err = loadBlocksFromPath(cmd.InOrStdin(), blocksPath)
				if err != nil {
					return err
				}
			} else if text != "" {
				for _, b := range render.TextToRichTextBlocks(text, render.RichTextOptions{}) {
					blocks = append(blocks, b)
				}
			}

			target, err := render.ParseTarget(args[0])
			if err != nil {
				return err
			}

			send := sendRequest{
				text:           formatted,
				blocks:         blocks,
				threadTS:       strings.TrimSpace(threadTS),
				replyBroadcast: replyBroadcast,
				attachPaths:    attachPaths,
				postAt:         postAt,
			}

			// Per-target send rules kept caller-side: DM targets reject
			// --reply-broadcast; channel targets need --thread-ts with it.
			switch target.Kind {
			case render.TargetURL:
				warnTruncatedURL(target.Ref)
			case render.TargetUser:
				if replyBroadcast {
					return agenterrors.New("--reply-broadcast is not supported for DM targets", agenterrors.FixableByAgent)
				}
			default:
				if replyBroadcast && send.threadTS == "" {
					return agenterrors.New("--reply-broadcast requires --thread-ts for channel targets", agenterrors.FixableByAgent)
				}
			}
			cc, channelID, err := resolveTargetClient(ctx, globals, target, openDMForUserTargets, "")
			if err != nil {
				return err
			}
			send.channelID = channelID
			if target.Kind == render.TargetURL {
				// Permalink sends always reply in that message's thread.
				send.threadTS, err = threadRootTS(ctx, cc, target.Ref, false)
				if err != nil {
					return err
				}
			}
			return runSend(ctx, globals, cc, send)
		},
	}
	cmd.Flags().StringVar(&threadTS, "thread-ts", "", "Thread root ts to post into")
	cmd.Flags().BoolVar(&replyBroadcast, "reply-broadcast", false, "Broadcast a thread reply to the channel")
	cmd.Flags().StringArrayVar(&attach, "attach", nil, "Attach a local file (repeatable)")
	cmd.Flags().StringVar(&blocksPath, "blocks", "", "Path to a JSON file with Block Kit blocks ('-' = stdin)")
	cmd.Flags().StringVar(&schedule, "schedule", "", "Schedule at an ISO 8601 time with timezone, or a unix timestamp")
	cmd.Flags().StringVar(&scheduleIn, "schedule-in", "", "Schedule after a duration (30m, 2d, tomorrow 9am, monday 9am)")
	parent.AddCommand(cmd)
}

type sendRequest struct {
	channelID      string
	text           string
	blocks         []any
	threadTS       string
	replyBroadcast bool
	attachPaths    []string
	postAt         int64
}

func runSend(ctx context.Context, globals *GlobalFlags, cc *clientContext, req sendRequest) error {
	if req.postAt != 0 {
		params := map[string]any{
			"channel": req.channelID,
			"text":    req.text,
			"post_at": req.postAt,
		}
		addThreadParams(params, req)
		resp, err := cc.Client.API(ctx, "chat.scheduleMessage", params)
		if err != nil {
			return err
		}
		channelID := req.channelID
		if id, ok := resp["channel"].(string); ok && id != "" {
			channelID = id
		}
		payload := map[string]any{
			"ok":         true,
			"channel_id": channelID,
			"post_at":    req.postAt,
		}
		if id, ok := resp["scheduled_message_id"].(string); ok {
			payload["scheduled_message_id"] = id
		}
		if at, ok := resp["post_at"].(float64); ok {
			payload["post_at"] = int64(at)
		}
		if req.threadTS != "" {
			payload["thread_ts"] = req.threadTS
		}
		return printSingle(globals, payload)
	}

	if len(req.attachPaths) == 0 {
		params := map[string]any{"channel": req.channelID, "text": req.text}
		addThreadParams(params, req)
		resp, err := cc.Client.API(ctx, "chat.postMessage", params)
		if err != nil {
			return err
		}
		channelID := req.channelID
		if id, ok := resp["channel"].(string); ok && id != "" {
			channelID = id
		}
		payload := map[string]any{"ok": true, "channel_id": channelID}
		if req.threadTS != "" {
			payload["thread_ts"] = req.threadTS
		}
		if ts, ok := resp["ts"].(string); ok && ts != "" {
			payload["ts"] = ts
			if cc.WorkspaceURL != "" {
				payload["permalink"] = render.BuildMessageURL(render.MessageURLParts{
					WorkspaceURL: cc.WorkspaceURL,
					ChannelID:    channelID,
					MessageTS:    ts,
					ThreadTS:     req.threadTS,
				})
			}
		}
		return printSingle(globals, payload)
	}

	if len(req.blocks) > 0 {
		_, _ = fmt.Fprintln(output.Stderr(), "Warning: rich text formatting is not supported with file attachments; sending as plain text.")
	}
	initialComment := req.text
	for _, path := range req.attachPaths {
		if err := cc.Client.UploadLocalFile(ctx, req.channelID, path, req.threadTS, initialComment); err != nil {
			return err
		}
		initialComment = ""
	}
	payload := map[string]any{"ok": true, "channel_id": req.channelID}
	if req.threadTS != "" {
		payload["thread_ts"] = req.threadTS
	}
	return printSingle(globals, payload)
}

func addThreadParams(params map[string]any, req sendRequest) {
	if req.threadTS != "" {
		params["thread_ts"] = req.threadTS
		if req.replyBroadcast {
			params["reply_broadcast"] = true
		}
	}
	if len(req.blocks) > 0 {
		params["blocks"] = req.blocks
	}
}

func loadBlocksFromPath(stdin io.Reader, path string) ([]any, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "--blocks: %v", err)
	}
	var parsed []any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "--blocks: expected a JSON array of Block Kit blocks: %v", err)
	}
	for i, el := range parsed {
		if _, ok := el.(map[string]any); !ok {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent, "--blocks: element at index %d is not a Block Kit block object", i)
		}
	}
	return parsed, nil
}

func dedupeStrings(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func registerMessageEdit(parent *cobra.Command, globals *GlobalFlags) {
	var ts string
	var yes bool
	cmd := &cobra.Command{
		Use:   "edit <target> <text>",
		Short: "Edit a message (destructive: requires --yes)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := requireYes(yes, fmt.Sprintf("would rewrite the message at %s with %d chars of new text", args[0], len(args[1]))); err != nil {
				return err
			}
			cc, ref, err := resolveMessageTarget(ctx, globals, args[0], ts, "")
			if err != nil {
				return err
			}
			params := map[string]any{
				"channel": ref.ChannelID,
				"ts":      ref.MessageTS,
				"text":    render.FormatOutboundText(args[1]),
			}
			if blocks := render.TextToRichTextBlocks(args[1], render.RichTextOptions{}); blocks != nil {
				params["blocks"] = toAnySlice(blocks)
			}
			if _, err := cc.Client.API(ctx, "chat.update", params); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"ok": true})
		},
	}
	cmd.Flags().StringVar(&ts, "ts", "", "Message ts (required when the target is a channel name/ID)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm the edit")
	parent.AddCommand(cmd)
}

func registerMessageDelete(parent *cobra.Command, globals *GlobalFlags) {
	var ts string
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <target>",
		Short: "Delete a message (destructive: requires --yes)",
		Args:  cobra.ExactArgs(1),
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
			if cancelChannel == "" {
				return agenterrors.New("--channel is required", agenterrors.FixableByAgent)
			}
			if err := requireYes(yes, fmt.Sprintf("would cancel scheduled message %s in %s", args[0], cancelChannel)); err != nil {
				return err
			}
			cc, channelID, err := resolveScheduledChannel(ctx, globals, cancelChannel)
			if err != nil {
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
	return resolveTargetClient(ctx, globals, target, openDMForUserTargets, "")
}
