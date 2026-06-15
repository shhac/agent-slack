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
			if len(args) == 1 {
				group, err := slack.GetUsergroup(ctx, cc.Client, args[0])
				if err != nil {
					return err
				}
				return printSingle(globals, group)
			}
			// Several args → resolve each, collecting inputs that don't resolve
			// rather than failing the batch (a typo doesn't drop the rest).
			var items []any
			var unresolved []string
			for _, arg := range args {
				group, err := slack.GetUsergroup(ctx, cc.Client, arg)
				if err != nil {
					unresolved = append(unresolved, arg)
					continue
				}
				items = append(items, group)
			}
			return printList(globals, items, unresolvedMeta(unresolved))
		},
	}
	parent.AddCommand(cmd)
}

func registerUsergroupMembers(parent *cobra.Command, globals *GlobalFlags) {
	var resolveUsers, refreshUsers, includeDisabled bool
	cmd := &cobra.Command{
		Use:               "members <usergroup>",
		Short:             "List the users in a usergroup (ids by default; --resolve-users for profiles)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: usergroupArgCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			ids, err := slack.ListUsergroupMembers(ctx, cc.Client, args[0], includeDisabled)
			if err != nil {
				return err
			}
			if !resolveUsers && !refreshUsers {
				items := make([]any, len(ids))
				for i, id := range ids {
					items[i] = map[string]any{"id": id}
				}
				return printList(globals, items, nil)
			}
			users := slack.ResolveUsersByID(ctx, cc.Client, ids, refreshUsers)
			items := make([]any, 0, len(ids))
			for _, id := range ids {
				if u, ok := users[id]; ok {
					items = append(items, u)
				} else {
					items = append(items, map[string]any{"id": id}) // profile fetch failed; keep the id
				}
			}
			return printList(globals, items, nil)
		},
	}
	cmd.Flags().BoolVar(&resolveUsers, "resolve-users", false, "Expand member ids to compact profiles")
	cmd.Flags().BoolVar(&refreshUsers, "refresh-users", false, "Bypass the user cache when resolving")
	cmd.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Allow members of a deactivated usergroup")
	parent.AddCommand(cmd)
}
