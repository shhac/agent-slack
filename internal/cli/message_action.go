package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

type messageActionFlags struct {
	ts       string
	actionID string
	blockID  string
	wait     time.Duration
	yes      bool
}

func registerMessageAction(parent *cobra.Command, globals *GlobalFlags) {
	flags := &messageActionFlags{}
	cmd := &cobra.Command{
		Use:               "action <target> [label]",
		Short:             "Press a button on an app's message, as clicking it in Slack would (requires --yes)",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if flags.wait < 0 {
				return agenterrors.New("--wait must not be negative", agenterrors.FixableByAgent).
					WithHint("pass a duration like 5s, or 0 to press without watching for the app's response")
			}
			sel := slack.ActionSelector{ActionID: flags.actionID, BlockID: flags.blockID}
			if len(args) == 2 {
				sel.Label = args[1]
			}
			cc, ref, err := resolveMessageTarget(ctx, globals, args[0], flags.ts, "")
			if err != nil {
				return err
			}
			if err := slack.RequireBlockActionAuth(cc.Client); err != nil {
				return err
			}
			msg, err := slack.FetchRawMessage(ctx, cc.Client, ref, false)
			if err != nil {
				return err
			}
			target, err := slack.SelectMessageAction(msg, sel)
			if err != nil {
				return err
			}
			if err := requireYes(flags.yes, describePress(msg, target, args[0])); err != nil {
				return err
			}
			res, err := slack.PressMessageAction(ctx, cc.Client, slack.PressInput{
				Ref:     ref,
				Message: msg,
				Target:  target,
				Wait:    flags.wait,
			})
			if err != nil {
				return err
			}
			return emitItem(globals, pressPayload(ctx, cc, ref, target, res))
		},
	}
	registerMessageTS(cmd, &flags.ts)
	cmd.Flags().StringVar(&flags.actionID, "action-id", "", "Select the element by its action_id (see 'actions' in 'message get')")
	cmd.Flags().StringVar(&flags.blockID, "block-id", "", "Narrow the selection to one block (see 'actions' in 'message get')")
	cmd.Flags().DurationVar(&flags.wait, "wait", 5*time.Second, "How long to watch for the app's response; 0 presses without watching")
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "Confirm the press")
	parent.AddCommand(cmd)
}

// pressPayload reports a completed press. An opened view is described and
// then closed: nothing here fills it in, and left open it would linger on
// the user's other clients.
func pressPayload(ctx context.Context, cc *clientContext, ref *render.MessageRef, target render.InteractiveElement, res slack.PressResult) map[string]any {
	payload := map[string]any{
		"pressed":    true,
		"channel_id": ref.ChannelID,
		"ts":         ref.MessageTS,
		"action":     render.CompactActionFor(target),
		"outcome":    res.Outcome,
	}
	if res.Message != nil {
		payload["message"] = render.ToCompactMessage(slack.SummaryFromRaw(ref.ChannelID, res.Message), render.CompactOptions{ActionOptions: true})
	}
	if res.View != nil {
		view := slack.DescribeView(slack.OpenedView(ctx, cc.Client, res.View))
		view.Closed = slack.CloseView(ctx, cc.Client, view.ID)
		payload["view"] = view
	}
	if len(res.Warnings) > 0 {
		payload["warnings"] = res.Warnings
	}
	return payload
}

// describePress names what a press would do, for the --yes gate: which
// element, on whose message. A press runs whatever the app wired to it, so
// the preview carries the element's own confirm warning when it has one.
func describePress(msg map[string]any, target render.InteractiveElement, targetInput string) string {
	app := slack.FirstNonEmpty(slack.SummaryFromRaw("", msg).BotName, "an app")
	desc := fmt.Sprintf("would press %s on %s's message at %s", slack.DescribeElement(target), app, targetInput)
	if confirm := render.CompactActionFor(target).Confirm; confirm != "" {
		desc += fmt.Sprintf(" (Slack would first ask: %q)", confirm)
	}
	return desc
}
