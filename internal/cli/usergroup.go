package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/slack"
)

func registerUsergroup(parent *cobra.Command, globals *GlobalFlags) {
	usergroupCmd := &cobra.Command{
		Use:     "usergroup",
		Aliases: []string{"usergroups"},
		Short:   "Workspace user groups (subteams)",
	}
	parent.AddCommand(usergroupCmd)
	handleUnknownSubcommand(usergroupCmd)

	registerUsergroupList(usergroupCmd, globals)
	registerUsergroupGet(usergroupCmd, globals)
	registerUsergroupMembers(usergroupCmd, globals)
}

func registerUsergroupList(parent *cobra.Command, globals *GlobalFlags) {
	var includeDisabled bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List usergroups (compact projection incl. default channels/groups)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			groups, err := slack.ListUsergroups(cmd.Context(), cc.Client, slack.ListUsergroupsOptions{
				IncludeDisabled: includeDisabled,
			})
			if err != nil {
				return err
			}
			return printList(globals, toAnySlice(groups), nil)
		},
	}
	cmd.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Include deactivated usergroups")
	parent.AddCommand(cmd)
}

func registerUsergroupGet(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:               "get <usergroup...>",
		Short:             "Get usergroups by id (S…) or @handle; one → object, several → NDJSON",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: usergroupArgsCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return runEntityGet(globals, args, func(arg string) (any, error) {
				return slack.GetUsergroup(ctx, cc.Client, arg)
			})
		},
	}
	parent.AddCommand(cmd)
}

func registerUsergroupMembers(parent *cobra.Command, globals *GlobalFlags) {
	var users string
	var includeDisabled bool
	cmd := &cobra.Command{
		Use:               "members <usergroup>",
		Short:             "List the users in a usergroup (ids by default; --users cached/fresh for profiles)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: usergroupArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			mode, err := parseUserMode(users)
			if err != nil {
				return err
			}
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			ids, err := slack.ListUsergroupMembers(ctx, cc.Client, args[0], includeDisabled)
			if err != nil {
				return err
			}
			return printMembers(ctx, globals, cc.Client, ids, mode, nil)
		},
	}
	registerUserMode(cmd, &users)
	cmd.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Allow members of a deactivated usergroup")
	parent.AddCommand(cmd)
}
