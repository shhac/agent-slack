package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerChannel(parent *cobra.Command, globals *GlobalFlags) {
	channelCmd := &cobra.Command{
		Use:   "channel",
		Short: "List conversations, create channels, and manage invites",
	}
	parent.AddCommand(channelCmd)
	handleUnknownSubcommand(channelCmd)
	registerChannelList(channelCmd, globals)
	registerChannelGet(channelCmd, globals)
	registerChannelMembers(channelCmd, globals)
	registerChannelNew(channelCmd, globals)
	registerChannelInvite(channelCmd, globals)
	registerChannelMark(channelCmd, globals)
}

func registerChannelGet(parent *cobra.Command, globals *GlobalFlags) {
	tflags := &transcriptFlags{}
	cmd := &cobra.Command{
		Use:               "get <channel...>",
		Short:             "Get channel metadata (topic, membership, archive state); one → object, several → NDJSON; --full for the raw object",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: channelArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if wantsTranscript(globals) {
				cc, err := getClient(globals)
				if err != nil {
					return err
				}
				channels, unresolved := collectEntityGet(args, func(arg string) (slack.CompactChannel, error) {
					return getChannelCompact(ctx, globals, arg)
				})
				return renderChannelsDigest(ctx, globals, cc, tflags, channels, unresolved, false)
			}
			return runEntityGet(globals, args, func(arg string) (any, error) {
				return getChannel(ctx, globals, arg)
			})
		},
	}
	enableTranscript(cmd, tflags)
	parent.AddCommand(cmd)
}

// getChannelCompact resolves one channel target to its compact projection,
// independent of --full (the transcript is always the compact, human view).
func getChannelCompact(ctx context.Context, globals *GlobalFlags, arg string) (slack.CompactChannel, error) {
	target, err := render.ParseTarget(arg)
	if err != nil {
		return slack.CompactChannel{}, err
	}
	cc, channelID, err := resolveTargetClient(ctx, globals, target, "channel get does not support user ID targets")
	if err != nil {
		return slack.CompactChannel{}, err
	}
	compact, _, err := slack.GetChannelInfo(ctx, cc.Client, channelID)
	return compact, err
}

// getChannel resolves one channel target and returns its compact projection
// (or the raw conversations.info object when --full is set).
func getChannel(ctx context.Context, globals *GlobalFlags, arg string) (any, error) {
	target, err := render.ParseTarget(arg)
	if err != nil {
		return nil, err
	}
	cc, channelID, err := resolveTargetClient(ctx, globals, target, "channel get does not support user ID targets")
	if err != nil {
		return nil, err
	}
	compact, raw, err := slack.GetChannelInfo(ctx, cc.Client, channelID)
	if err != nil {
		return nil, err
	}
	if globals.Full {
		return raw, nil
	}
	return compact, nil
}

func registerChannelMembers(parent *cobra.Command, globals *GlobalFlags) {
	var limit int
	var cursor string
	var resolveFlag string
	cmd := &cobra.Command{
		Use:               "members <channel>",
		Short:             "List the users in a channel (ids by default; --resolve cached/fresh for profiles)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: channelArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			mode, err := parseResolveMode(resolveFlag)
			if err != nil {
				return err
			}
			target, err := render.ParseTarget(args[0])
			if err != nil {
				return err
			}
			cc, channelID, err := resolveTargetClient(ctx, globals, target, "channel members does not support user ID targets")
			if err != nil {
				return err
			}
			ids, next, err := slack.ListChannelMembers(ctx, cc.Client, channelID, limit, cursor)
			if err != nil {
				return err
			}
			meta := listMeta(next, map[string]any{"channel_id": channelID})
			return printMembers(ctx, globals, cc.Client, ids, mode, meta)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "Max members per page")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor")
	registerResolveFlag(cmd, &resolveFlag, resolveNone)
	parent.AddCommand(cmd)
}

func registerChannelList(parent *cobra.Command, globals *GlobalFlags) {
	var user, cursor string
	var all bool
	var limit int
	tflags := &transcriptFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List conversations for a user (default: the authed user), or --all for the workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if all && user != "" {
				return agenterrors.New("--all cannot be used with --user", agenterrors.FixableByAgent)
			}
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			opts := slack.ConversationsOptions{All: all, Limit: limit, Cursor: cursor}
			if !all && user != "" {
				opts.User, err = slack.ResolveUserID(ctx, cc.Client, user)
				if err != nil {
					return err
				}
			}
			page, err := slack.ListConversations(ctx, cc.Client, opts)
			if err != nil {
				return err
			}

			if wantsTranscript(globals) {
				compacts := make([]slack.CompactChannel, 0, len(page.Channels))
				for _, ch := range page.Channels {
					compacts = append(compacts, slack.ToCompactChannel(ch))
				}
				return renderChannelsDigest(ctx, globals, cc, tflags, compacts, nil, page.NextCursor != "")
			}
			items := make([]any, 0, len(page.Channels))
			for _, ch := range page.Channels {
				if globals.Full {
					items = append(items, ch)
					continue
				}
				items = append(items, slack.ToCompactChannel(ch))
			}
			return printList(globals, items, listMeta(page.NextCursor, nil))
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "User id (U…) or @handle whose conversations to list")
	cmd.Flags().BoolVar(&all, "all", false, "List all workspace conversations (conversations.list)")
	cmd.Flags().IntVar(&limit, "limit", 100, "Max conversations per page")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor")
	enableTranscript(cmd, tflags)
	parent.AddCommand(cmd)
}

func registerChannelNew(parent *cobra.Command, globals *GlobalFlags) {
	var name string
	var private, yes bool
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a channel (requires --yes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			visibility := "public"
			if private {
				visibility = "private"
			}
			if err := requireYes(yes, fmt.Sprintf("would create %s channel #%s", visibility, strings.TrimSpace(name))); err != nil {
				return err
			}
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			created, err := slack.CreateChannel(cmd.Context(), cc.Client, name, private)
			if err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"channel": created})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Channel name (required)")
	cmd.Flags().BoolVar(&private, "private", false, "Create as a private channel")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm the creation")
	_ = cmd.MarkFlagRequired("name")
	parent.AddCommand(cmd)
}

func registerChannelInvite(parent *cobra.Command, globals *GlobalFlags) {
	var channel, users string
	var external, allowExternalUserInvites, yes bool
	cmd := &cobra.Command{
		Use:   "invite",
		Short: "Invite users to a channel (requires --yes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if allowExternalUserInvites && !external {
				return agenterrors.New("--allow-external-user-invites requires --external", agenterrors.FixableByAgent)
			}
			userInputs := slack.ParseInviteUsersCSV(users)
			if len(userInputs) == 0 {
				return agenterrors.New("no users provided", agenterrors.FixableByAgent).
					WithHint(`pass --users "U01…,@alice,bob@example.com"`)
			}
			kind := "invite"
			if external {
				kind = "Slack Connect external invite"
			}
			if err := requireYes(yes, fmt.Sprintf("would %s %d user(s) to %s", kind, len(userInputs), channel)); err != nil {
				return err
			}

			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			channelID, err := slack.ResolveChannelID(ctx, cc.Client, channel)
			if err != nil {
				return err
			}

			var payload any
			if external {
				payload, err = inviteExternal(ctx, cc.Client, channelID, userInputs, allowExternalUserInvites)
			} else {
				payload, err = inviteInternal(ctx, cc.Client, channelID, userInputs)
			}
			if err != nil {
				return err
			}
			return printSingle(globals, payload)
		},
	}
	cmd.Flags().StringVar(&channel, "channel", "", "Channel id or name (required)")
	cmd.Flags().StringVar(&users, "users", "", "Comma-separated users: U…, @handle, or email (required)")
	cmd.Flags().BoolVar(&external, "external", false, "Send Slack Connect external invites (email targets only)")
	cmd.Flags().BoolVar(&allowExternalUserInvites, "allow-external-user-invites", false, "Allow external invitees to invite others")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm the invite")
	_ = cmd.MarkFlagRequired("channel")
	registerFlagCompletion(cmd, "channel", globals, slack.CompleteChannels)
	_ = cmd.MarkFlagRequired("users")
	parent.AddCommand(cmd)
}

// inviteExternal sends Slack Connect (email) invites to the channel.
func inviteExternal(ctx context.Context, client *slack.Client, channelID string, userInputs []string, allowExternalUserInvites bool) (any, error) {
	emails, nonEmails := slack.SplitEmailsFromInviteTargets(userInputs)
	if len(emails) == 0 {
		return nil, agenterrors.New("external invites require email targets in --users", agenterrors.FixableByAgent).
			WithHint(`e.g. --users "alice@example.com,bob@example.com"`)
	}
	externalLimited := !allowExternalUserInvites
	result, err := slack.InviteExternalUsersToChannel(ctx, client, channelID, emails, externalLimited)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"channel_id":               channelID,
		"external":                 true,
		"external_limited":         externalLimited,
		"invited_emails":           result.InvitedEmails,
		"already_invited_emails":   result.AlreadyInvitedEmails,
		"invalid_external_targets": nonEmails,
	}, nil
}

// inviteInternal resolves the user targets to ids and adds them to the channel.
func inviteInternal(ctx context.Context, client *slack.Client, channelID string, userInputs []string) (any, error) {
	resolved, unresolved := resolveUserIDs(ctx, client, userInputs)
	result, err := slack.InviteUsersToChannel(ctx, client, channelID, resolved)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"channel_id":                  channelID,
		"invited_user_ids":            result.InvitedUserIDs,
		"already_in_channel_user_ids": result.AlreadyInChannelUserIDs,
		"unresolved_users":            unresolved,
	}, nil
}

// resolveUserIDs resolves each input (U…, @handle, or email) to a user id,
// collecting the inputs that don't resolve.
func resolveUserIDs(ctx context.Context, client *slack.Client, inputs []string) (resolved, unresolved []string) {
	for _, input := range inputs {
		id, err := slack.ResolveUserID(ctx, client, input)
		if err != nil {
			unresolved = append(unresolved, input)
			continue
		}
		resolved = append(resolved, id)
	}
	return resolved, unresolved
}

func registerChannelMark(parent *cobra.Command, globals *GlobalFlags) {
	var ts string
	cmd := &cobra.Command{
		Use:               "mark <target>",
		Short:             "Mark a channel/DM read up to a message",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			target, err := render.ParseTarget(args[0])
			if err != nil {
				return err
			}
			// mark-specific rules before the shared resolution: a URL target
			// carries its own workspace (and default ts); channel targets
			// need an explicit --ts.
			markTS := strings.TrimSpace(ts)
			switch target.Kind {
			case render.TargetURL:
				if globals.Workspace != "" {
					return agenterrors.New("--workspace cannot be used with a URL target; the workspace comes from the URL", agenterrors.FixableByAgent)
				}
				if markTS == "" {
					markTS = target.Ref.MessageTS
				}
			case render.TargetChannel:
				if markTS == "" {
					return agenterrors.New("--ts is required when the target is a channel name or ID", agenterrors.FixableByAgent)
				}
			}
			cc, channelID, err := resolveTargetClient(ctx, globals, target, "user targets are not supported for channel mark")
			if err != nil {
				return err
			}
			if err := slack.MarkConversation(ctx, cc.Client, channelID, markTS); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"ok": true, "channel": channelID, "ts": markTS})
		},
	}
	cmd.Flags().StringVar(&ts, "ts", "", "Message ts to mark read up to (required for channel targets)")
	parent.AddCommand(cmd)
}
