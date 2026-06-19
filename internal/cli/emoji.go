package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/slack"
)

func registerEmoji(parent *cobra.Command, globals *GlobalFlags) {
	emojiCmd := &cobra.Command{
		Use:   "emoji",
		Short: "Workspace custom emoji (:shortcode:)",
	}
	parent.AddCommand(emojiCmd)
	handleUnknownSubcommand(emojiCmd)

	registerEmojiList(emojiCmd, globals)
	registerEmojiGet(emojiCmd, globals)
	registerEmojiSearch(emojiCmd, globals)
}

func registerEmojiList(parent *cobra.Command, globals *GlobalFlags) {
	var full bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the workspace's custom emoji (names + aliases; --full adds image URLs)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			emoji, err := slack.ListEmoji(cmd.Context(), cc.Client, slack.ListEmojiOptions{Full: full})
			if err != nil {
				return err
			}
			return printList(globals, toAnySlice(emoji), nil)
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "Include image URLs (omitted by default to keep output lean)")
	parent.AddCommand(cmd)
}

func registerEmojiGet(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "get <emoji...>",
		Short: "Resolve emoji by name (custom or standard, :colons: optional); one → object, several → NDJSON",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			return runEntityGet(globals, args, func(arg string) (any, error) {
				return slack.GetEmoji(ctx, cc.Client, arg)
			})
		},
	}
	parent.AddCommand(cmd)
}

func registerEmojiSearch(parent *cobra.Command, globals *GlobalFlags) {
	var limit int
	var cursor string
	var full bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Fuzzy-search custom emoji by name, ranked (exact → prefix → token → substring → fuzzy); paginated",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			matches, next, err := slack.SearchEmoji(cmd.Context(), cc.Client, args[0], slack.SearchEmojiOptions{
				Limit:  limit,
				Cursor: cursor,
				Full:   full,
			})
			if err != nil {
				return err
			}
			return printList(globals, toAnySlice(matches), listMeta(next, nil))
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Max results per page (capped at 100)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor from a prior page's @pagination.next_cursor")
	cmd.Flags().BoolVar(&full, "full", false, "Include image URLs in results")
	parent.AddCommand(cmd)
}
