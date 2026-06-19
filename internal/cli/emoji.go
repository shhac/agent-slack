package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
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
	registerEmojiAdd(emojiCmd, globals)
	registerEmojiRemove(emojiCmd, globals)
}

func registerEmojiList(parent *cobra.Command, globals *GlobalFlags) {
	var full bool
	var limit int
	var cursor string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the workspace's custom emoji (names + aliases; --full adds image URLs); paginated",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			emoji, next, err := slack.ListEmoji(cmd.Context(), cc.Client, slack.ListEmojiOptions{
				Full:   full,
				Limit:  limit,
				Cursor: cursor,
			})
			if err != nil {
				return err
			}
			return printList(globals, toAnySlice(emoji), listMeta(next, nil))
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "Include image URLs (omitted by default to keep output lean)")
	cmd.Flags().IntVar(&limit, "limit", 200, "Max results per page (capped at 1000)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor from a prior page's @pagination.next_cursor")
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

func registerEmojiAdd(parent *cobra.Command, globals *GlobalFlags) {
	var image, aliasFor string
	var yes bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a custom emoji from an image (--image) or as an alias (--alias-for); requires --yes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if (image == "") == (aliasFor == "") {
				return agenterrors.New("provide exactly one of --image or --alias-for", agenterrors.FixableByAgent).
					WithHint("--image <path> uploads an image; --alias-for <name> points at an existing emoji")
			}
			what := fmt.Sprintf("would add :%s: from image %q", name, image)
			if aliasFor != "" {
				what = fmt.Sprintf("would add :%s: as an alias for :%s:", name, aliasFor)
			}
			if err := requireYes(yes, what); err != nil {
				return err
			}
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			var stored string
			if aliasFor != "" {
				stored, err = slack.AddEmojiAlias(ctx, cc.Client, name, aliasFor)
			} else {
				stored, err = slack.AddEmoji(ctx, cc.Client, name, image)
			}
			if err != nil {
				return err
			}
			payload := map[string]any{"added": stored}
			if aliasFor != "" {
				payload["alias_for"] = aliasFor
			}
			return printSingle(globals, payload)
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "Path to a local png/gif/jpeg/webp image")
	cmd.Flags().StringVar(&aliasFor, "alias-for", "", "Existing emoji name this should alias")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm the addition")
	parent.AddCommand(cmd)
}

func registerEmojiRemove(parent *cobra.Command, globals *GlobalFlags) {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a custom emoji by name (destructive: requires --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := requireYes(yes, fmt.Sprintf("would remove custom emoji :%s:", name)); err != nil {
				return err
			}
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			stored, err := slack.RemoveEmoji(cmd.Context(), cc.Client, name)
			if err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"removed": stored})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm the removal")
	parent.AddCommand(cmd)
}
