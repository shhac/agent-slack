package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/slack"
)

func registerUser(parent *cobra.Command, globals *GlobalFlags) {
	userCmd := &cobra.Command{
		Use:   "user",
		Short: "Workspace user directory",
	}
	parent.AddCommand(userCmd)
	handleUnknownSubcommand(userCmd)

	var limit int
	var cursor string
	var includeBots bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List users (compact projection; each includes dm_id when a DM exists)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			page, err := slack.ListUsers(cmd.Context(), cc.Client, slack.ListUsersOptions{
				Limit:       limit,
				Cursor:      cursor,
				IncludeBots: includeBots,
			})
			if err != nil {
				return err
			}
			return printList(globals, toAnySlice(page.Users), listMeta(page.NextCursor, nil))
		},
	}
	listCmd.Flags().IntVar(&limit, "limit", 200, "Max users")
	listCmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor")
	listCmd.Flags().BoolVar(&includeBots, "include-bots", false, "Include bot users")
	userCmd.AddCommand(listCmd)

	getCmd := &cobra.Command{
		Use:               "get <user...>",
		Short:             "Get users by id (U…), @handle, or email; one → object, several → NDJSON",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: cacheCompletion(globals, slack.CompleteUsers, false),
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return runEntityGet(globals, args, func(arg string) (any, error) {
				return slack.GetUser(ctx, cc.Client, arg)
			})
		},
	}
	userCmd.AddCommand(getCmd)

	dmOpenCmd := &cobra.Command{
		Use:               "dm-open <users...>",
		Short:             "Open (or get) a DM / group DM channel for one or more users",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: userArgsCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			result, err := slack.GetDMChannelForUsers(cmd.Context(), cc.Client, args)
			if err != nil {
				return err
			}
			return printSingle(globals, result)
		},
	}
	userCmd.AddCommand(dmOpenCmd)
}
