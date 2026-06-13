package cli

import (
	"strings"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerWorkflow(parent *cobra.Command, globals *GlobalFlags) {
	workflowCmd := &cobra.Command{
		Use:   "workflow",
		Short: "Discover and run Slack workflows",
	}
	parent.AddCommand(workflowCmd)
	handleUnknownSubcommand(workflowCmd)

	registerWorkflowList(workflowCmd, globals)
	registerWorkflowPreview(workflowCmd, globals)
	registerWorkflowGet(workflowCmd, globals)
	registerWorkflowRun(workflowCmd, globals)
}

func registerWorkflowList(parent *cobra.Command, globals *GlobalFlags) {
	listCmd := &cobra.Command{
		Use:   "list <channel>",
		Short: "List workflows bookmarked or featured in a channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			channelID, err := slack.ResolveChannelID(ctx, cc.Client, args[0])
			if err != nil {
				return err
			}
			result, err := slack.ListChannelWorkflows(ctx, cc.Client, channelID)
			if err != nil {
				return err
			}
			return printList(globals, toAnySlice(result.Workflows),
				listMeta("", map[string]any{"channel_id": result.ChannelID}))
		},
	}
	parent.AddCommand(listCmd)
}

func registerWorkflowPreview(parent *cobra.Command, globals *GlobalFlags) {
	previewCmd := &cobra.Command{
		Use:   "preview <trigger-id>",
		Short: "Get workflow metadata from a trigger ID (no side effects)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			preview, err := slack.PreviewWorkflowTrigger(cmd.Context(), cc.Client, args[0])
			if err != nil {
				return err
			}
			return printSingle(globals, preview)
		},
	}
	parent.AddCommand(previewCmd)
}

func registerWorkflowGet(parent *cobra.Command, globals *GlobalFlags) {
	getCmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Get a workflow definition (form fields + steps) by Ft… trigger or Wf… workflow id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			workflowID := args[0]
			if strings.HasPrefix(workflowID, "Ft") {
				preview, err := slack.PreviewWorkflowTrigger(ctx, cc.Client, workflowID)
				if err != nil {
					return err
				}
				workflowID = preview.Workflow.ID
			}
			schema, err := slack.GetWorkflowSchema(ctx, cc.Client, workflowID)
			if err != nil {
				return err
			}
			return printSingle(globals, schema)
		},
	}
	parent.AddCommand(getCmd)
}

func registerWorkflowRun(parent *cobra.Command, globals *GlobalFlags) {
	var channel string
	var fields []string
	runCmd := &cobra.Command{
		Use:   "run <trigger-id>",
		Short: "Trip a workflow trigger; with --field Title=value, submits its form",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			triggerID := args[0]
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			channelID, err := slack.ResolveChannelID(ctx, cc.Client, channel)
			if err != nil {
				return err
			}

			if len(fields) == 0 {
				shortcut, err := slack.ResolveShortcut(ctx, cc.Client, channelID, triggerID)
				if err != nil {
					return err
				}
				result, err := slack.RunWorkflowTrigger(ctx, cc.Client, shortcut.URL, channelID, shortcut.BookmarkID)
				if err != nil {
					return err
				}
				return printSingle(globals, map[string]any{"ok": true, "run": result})
			}

			fieldValues := map[string]string{}
			for _, arg := range fields {
				title, value, found := strings.Cut(arg, "=")
				if !found || title == "" {
					return agenterrors.Newf(agenterrors.FixableByAgent, "invalid --field format: %q", arg).
						WithHint("expected Title=value; 'workflow get <trigger-id>' lists field titles")
				}
				fieldValues[title] = value
			}

			preview, err := slack.PreviewWorkflowTrigger(ctx, cc.Client, triggerID)
			if err != nil {
				return err
			}
			schema, err := slack.GetWorkflowSchema(ctx, cc.Client, preview.Workflow.ID)
			if err != nil {
				return err
			}
			if errs := slack.ValidateWorkflowFields(fieldValues, schema); len(errs) > 0 {
				return agenterrors.New(strings.Join(errs, "; "), agenterrors.FixableByAgent).
					WithHint("'agent-slack workflow get " + triggerID + "' shows the form schema")
			}
			shortcut, err := slack.ResolveShortcut(ctx, cc.Client, channelID, triggerID)
			if err != nil {
				return err
			}
			result, err := slack.SubmitWorkflowForm(ctx, cc.Client, slack.WorkflowSubmission{
				ShortcutURL: shortcut.URL,
				ChannelID:   channelID,
				BookmarkID:  shortcut.BookmarkID,
				Fields:      fieldValues,
				Schema:      schema,
			})
			if err != nil {
				return err
			}
			return printSingle(globals, result)
		},
	}
	runCmd.Flags().StringVar(&channel, "channel", "", "Channel where the workflow is bookmarked (required)")
	runCmd.Flags().StringArrayVar(&fields, "field", nil, "Form field value as Title=value (repeatable)")
	_ = runCmd.MarkFlagRequired("channel")
	parent.AddCommand(runCmd)
}
