package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/slack"
)

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
