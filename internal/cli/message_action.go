package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
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
	value    string
	fields   []string
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
			fields, err := parseFieldArgs(flags.fields)
			if err != nil {
				return err
			}
			if len(fields) > 0 && flags.wait == 0 {
				return agenterrors.New("--field needs --wait above 0: the form is only seen by watching for it", agenterrors.FixableByAgent).
					WithHint("drop --wait 0, or pass --wait 5s")
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
			choice, err := slack.ActionChoice(target, flags.value)
			if err != nil {
				return err
			}
			if err := requireYes(flags.yes, describePress(msg, target, flags.value, fields, args[0])); err != nil {
				return err
			}
			res, err := slack.PressMessageAction(ctx, cc.Client, slack.PressInput{
				Ref:     ref,
				Message: msg,
				Target:  target,
				Choice:  choice,
				Wait:    flags.wait,
			})
			if err != nil {
				return err
			}
			view, warnings := settleView(ctx, cc.Client, res, fields)
			return emitItem(globals, pressPayload(ref, target, res, view, warnings))
		},
	}
	registerMessageTS(cmd, &flags.ts)
	cmd.Flags().StringVar(&flags.actionID, "action-id", "", "Select the element by its action_id (see 'actions' in 'message get')")
	cmd.Flags().StringVar(&flags.blockID, "block-id", "", "Narrow the selection to one block (see 'actions' in 'message get')")
	cmd.Flags().StringVar(&flags.value, "value", "", "A menu or picker's choice: an option label or value, a YYYY-MM-DD date, HH:MM time, or U…/C… id (comma-separate several)")
	cmd.Flags().StringArrayVar(&flags.fields, "field", nil, "Fill the form the press opens: Title=value, by the field's label (repeatable; unnamed fields keep their current values)")
	cmd.Flags().DurationVar(&flags.wait, "wait", 5*time.Second, "How long to watch for the app's response; 0 presses without watching")
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "Confirm the press")
	parent.AddCommand(cmd)
}

// settleView handles the view a press opened, if any. Without one, fields
// that were given had nothing to fill — said in a warning, since the press
// itself still happened.
func settleView(ctx context.Context, c *slack.Client, res slack.PressResult, fields map[string]string) (*slack.ViewSummary, []string) {
	warnings := res.Warnings
	if res.View == nil {
		if len(fields) > 0 {
			warnings = append(warnings, "--field was given but the app opened no form, so nothing was filled in")
		}
		return nil, warnings
	}
	view, warning := slack.SettleOpenedView(ctx, c, res.View, fields)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	return &view, warnings
}

// pressPayload shapes a completed press for output.
func pressPayload(ref *render.MessageRef, target render.InteractiveElement, res slack.PressResult, view *slack.ViewSummary, warnings []string) map[string]any {
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
	if view != nil {
		payload["view"] = view
	}
	if len(warnings) > 0 {
		payload["warnings"] = warnings
	}
	return payload
}

// describePress names what a press would do, for the --yes gate: which
// element, on whose message. A press runs whatever the app wired to it, so
// the preview carries the element's own confirm warning when it has one.
func describePress(msg map[string]any, target render.InteractiveElement, value string, fields map[string]string, targetInput string) string {
	app := slack.FirstNonEmpty(slack.SummaryFromRaw("", msg).BotName, "an app")
	verb := "press"
	if value != "" {
		verb = fmt.Sprintf("choose %q in", value)
	}
	desc := fmt.Sprintf("would %s %s on %s's message at %s", verb, slack.DescribeElement(target), app, targetInput)
	if len(fields) > 0 {
		desc += fmt.Sprintf(", then submit the form it opens with %s", strings.Join(slices.Sorted(maps.Keys(fields)), ", "))
	}
	if confirm := render.CompactActionFor(target).Confirm; confirm != "" {
		desc += fmt.Sprintf(" (Slack would first ask: %q)", confirm)
	}
	return desc
}
