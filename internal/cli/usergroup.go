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
	var limit int
	var cursor string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List usergroups (compact projection incl. default channels/groups); paginated",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			groups, next, err := slack.ListUsergroups(cmd.Context(), cc.Client, slack.ListUsergroupsOptions{
				IncludeDisabled: includeDisabled,
				Limit:           limit,
				Cursor:          cursor,
			})
			if err != nil {
				return err
			}
			return printList(globals, toAnySlice(groups), listMeta(next, nil))
		},
	}
	cmd.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Include deactivated usergroups")
	cmd.Flags().IntVar(&limit, "limit", 200, "Max results per page (capped at 1000)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor from a prior page's @pagination.next_cursor")
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
	var resolveFlag string
	var includeDisabled bool
	cmd := &cobra.Command{
		Use:               "members <usergroup>",
		Short:             "List the users in a usergroup (ids by default; --resolve cached/fresh for profiles)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: usergroupArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			mode, err := parseResolveMode(resolveFlag)
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
	registerResolveFlag(cmd, &resolveFlag, resolveNone)
	cmd.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Allow members of a deactivated usergroup")
	parent.AddCommand(cmd)
}
