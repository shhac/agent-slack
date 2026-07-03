package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerLater(parent *cobra.Command, globals *GlobalFlags) {
	laterCmd := &cobra.Command{
		Use:   "later",
		Short: "Manage saved-for-later messages (Slack's Later tab; browser auth)",
	}
	parent.AddCommand(laterCmd)
	handleUnknownSubcommand(laterCmd)

	registerLaterList(laterCmd, globals)
	registerLaterMark(laterCmd, globals, "save", "Save a message for later", func(ctx context.Context, c *slack.Client, channelID, ts string) error {
		return slack.SaveLater(ctx, c, channelID, ts)
	})
	registerLaterMark(laterCmd, globals, "complete", "Mark a saved message as completed", func(ctx context.Context, c *slack.Client, channelID, ts string) error {
		return slack.UpdateLaterMark(ctx, c, channelID, ts, "completed")
	})
	registerLaterMark(laterCmd, globals, "archive", "Archive a saved message", func(ctx context.Context, c *slack.Client, channelID, ts string) error {
		return slack.UpdateLaterMark(ctx, c, channelID, ts, "archived")
	})
	registerLaterMark(laterCmd, globals, "reopen", "Move a saved message back to in-progress", func(ctx context.Context, c *slack.Client, channelID, ts string) error {
		// The current state is unknown, so undo both (the TS does the same).
		err1 := slack.UpdateLaterMark(ctx, c, channelID, ts, "uncompleted")
		err2 := slack.UpdateLaterMark(ctx, c, channelID, ts, "unarchived")
		if err1 != nil && err2 != nil {
			return err1
		}
		return nil
	})
	registerLaterMark(laterCmd, globals, "remove", "Remove a message from Later entirely", func(ctx context.Context, c *slack.Client, channelID, ts string) error {
		return slack.RemoveLater(ctx, c, channelID, ts)
	})
	registerLaterRemind(laterCmd, globals)
}

func registerLaterList(parent *cobra.Command, globals *GlobalFlags) {
	var state, cursor string
	var limit, maxBodyChars int
	var countsOnly, slackMarkdown bool
	tflags := &transcriptFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List saved-for-later messages",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			parsedState, err := slack.ParseLaterState(state)
			if err != nil {
				return err
			}
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			result, err := slack.FetchLaterItems(cmd.Context(), cc.Client, slack.LaterOptions{
				State:         parsedState,
				Limit:         limit,
				MaxBodyChars:  maxBodyChars,
				SlackMarkdown: slackMarkdown,
				CountsOnly:    countsOnly,
				Cursor:        cursor,
			})
			if err != nil {
				return err
			}
			if wantsTranscript(globals) {
				return renderLaterTranscript(cmd.Context(), globals, cc, tflags, result.Items)
			}
			meta := listMeta(result.NextCursor, map[string]any{"counts": result.Counts})
			return printList(globals, toAnySlice(result.Items), meta)
		},
	}
	enableTranscript(cmd, tflags)
	registerResolveFlag(cmd, &tflags.resolve, resolveAuto)
	cmd.Flags().StringVar(&state, "state", "in_progress", "Filter: in_progress|archived|completed|all")
	_ = cmd.RegisterFlagCompletionFunc("state", fixedCompletions("in_progress", "archived", "completed", "all"))
	cmd.Flags().IntVar(&limit, "limit", 20, "Max items")
	cmd.Flags().IntVar(&maxBodyChars, "max-body-chars", 4000, "Max content chars per item (-1 = unlimited)")
	cmd.Flags().BoolVar(&countsOnly, "counts-only", false, "Only counts per state, no content")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor")
	cmd.Flags().BoolVar(&slackMarkdown, "slack-markdown", false, "Render content as Slack mrkdwn instead of standard Markdown")
	parent.AddCommand(cmd)
}

// laterTarget resolves a later subcommand's <target> + --ts to channel + ts.
func laterTarget(ctx context.Context, globals *GlobalFlags, targetInput, ts string) (*clientContext, string, string, error) {
	cc, ref, err := resolveMessageTarget(ctx, globals, targetInput, ts, "")
	if err != nil {
		return nil, "", "", err
	}
	return cc, ref.ChannelID, ref.MessageTS, nil
}

func registerLaterMark(parent *cobra.Command, globals *GlobalFlags, name, short string, action func(ctx context.Context, c *slack.Client, channelID, ts string) error) {
	var ts string
	cmd := &cobra.Command{
		Use:   name + " <target>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, channelID, messageTS, err := laterTarget(ctx, globals, args[0], ts)
			if err != nil {
				return err
			}
			if err := action(ctx, cc.Client, channelID, messageTS); err != nil {
				return err
			}
			return printOK(globals)
		},
	}
	cmd.Flags().StringVar(&ts, "ts", "", "Message ts (required when the target is a channel ID)")
	parent.AddCommand(cmd)
}

func registerLaterRemind(parent *cobra.Command, globals *GlobalFlags) {
	var ts, in string
	cmd := &cobra.Command{
		Use:               "remind <target>",
		Short:             "Set a reminder on a saved message",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(in) == "" {
				return agenterrors.New("--in is required", agenterrors.FixableByAgent).
					WithHint("e.g. --in 3h, --in \"tomorrow 5pm\", --in \"next friday\"")
			}
			remindAt, err := slack.ParseReminderDuration(in, time.Now())
			if err != nil {
				return err
			}
			cc, channelID, messageTS, err := laterTarget(ctx, globals, args[0], ts)
			if err != nil {
				return err
			}
			if err := slack.SetLaterReminder(ctx, cc.Client, channelID, messageTS, remindAt); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"ok": true, "remind_at": remindAt})
		},
	}
	cmd.Flags().StringVar(&ts, "ts", "", "Message ts (required when the target is a channel ID)")
	cmd.Flags().StringVar(&in, "in", "", "When to remind: 30m, 1h, 2d, tomorrow 5pm, next friday (required)")
	parent.AddCommand(cmd)
}
