package cli

import (
	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerSearch(parent *cobra.Command, globals *GlobalFlags) {
	searchCmd := &cobra.Command{
		Use:   "search",
		Short: "Search Slack messages and files (token-efficient JSON)",
	}
	parent.AddCommand(searchCmd)
	handleUnknownSubcommand(searchCmd)
	registerSearchKind(searchCmd, globals, "all", slack.SearchAll, "Search messages and files")
	registerSearchKind(searchCmd, globals, "messages", slack.SearchMessages, "Search messages")
	registerSearchKind(searchCmd, globals, "files", slack.SearchFiles, "Search files (downloads matches and reports local paths)")
}

func registerSearchKind(parent *cobra.Command, globals *GlobalFlags, name string, kind slack.SearchKind, short string) {
	var channels []string
	var user, after, before, contentType string
	var limit, maxContentChars int
	var download, resolveUsers, refreshUsers bool

	cmd := &cobra.Command{
		Use:   name + " <query>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch contentType {
			case "any", "text", "image", "snippet", "file":
			default:
				return agenterrors.Newf(agenterrors.FixableByAgent, "invalid --content-type %q", contentType).
					WithHint("use any, text, image, snippet, or file")
			}
			if !download && kind != slack.SearchMessages {
				return agenterrors.New("file search requires downloads (agents need local file paths)", agenterrors.FixableByAgent).
					WithHint("drop --download=false or use 'search messages'")
			}

			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			result, err := slack.Search(cmd.Context(), cc.Client, slack.SearchOptions{
				WorkspaceURL:    cc.WorkspaceURL,
				Query:           args[0],
				Kind:            kind,
				Channels:        channels,
				User:            user,
				After:           after,
				Before:          before,
				ContentType:     slack.ContentType(contentType),
				Limit:           limit,
				MaxContentChars: maxContentChars,
				Download:        download,
				ResolveUsers:    resolveUsers,
				RefreshUsers:    refreshUsers,
				DownloadsDir:    downloadsDir(),
				UserCacheDir:    appCacheDir(),
				Warn:            globals.stderr,
			})
			if err != nil {
				return err
			}

			var items []any
			for _, m := range result.Messages {
				items = append(items, m)
			}
			for _, f := range result.Files {
				items = append(items, map[string]any{"file": f})
			}
			var extra map[string]any
			if result.ReferencedUsers != nil {
				extra = map[string]any{"referenced_users": result.ReferencedUsers}
			}
			return printList(globals, items, listMeta("", extra))
		},
	}
	cmd.Flags().StringArrayVar(&channels, "channel", nil, "Channel filter (#name, name, or id; repeatable)")
	cmd.Flags().StringVar(&user, "user", "", "Author filter (@name, name, or U…)")
	cmd.Flags().StringVar(&after, "after", "", "Only results after YYYY-MM-DD")
	cmd.Flags().StringVar(&before, "before", "", "Only results before YYYY-MM-DD")
	cmd.Flags().StringVar(&contentType, "content-type", "any", "Filter: any|text|image|snippet|file")
	cmd.Flags().IntVar(&limit, "limit", 20, "Max results")
	cmd.Flags().IntVar(&maxContentChars, "max-content-chars", 4000, "Max message content chars (-1 = unlimited)")
	cmd.Flags().BoolVar(&download, "download", kind != slack.SearchMessages, "Download matched files and report local paths")
	cmd.Flags().BoolVar(&resolveUsers, "resolve-users", false, "Resolve referenced user IDs to profiles")
	cmd.Flags().BoolVar(&refreshUsers, "refresh-users", false, "Refresh the user cache before resolving")
	parent.AddCommand(cmd)
}
