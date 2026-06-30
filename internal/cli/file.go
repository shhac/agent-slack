package cli

import (
	"strings"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerFile(parent *cobra.Command, globals *GlobalFlags) {
	fileCmd := &cobra.Command{
		Use:   "file",
		Short: "Point-pull Slack files seen in message/search output",
	}
	parent.AddCommand(fileCmd)
	handleUnknownSubcommand(fileCmd)

	downloadCmd := &cobra.Command{
		Use:   "download <file-id>",
		Short: "Download one file by id (F…) to the cache dir and print its local path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			fileID := strings.TrimSpace(args[0])
			if !strings.HasPrefix(fileID, "F") {
				return agenterrors.Newf(agenterrors.FixableByAgent, "not a Slack file id: %s", args[0]).
					WithHint("file ids start with F and appear in message/search file metadata")
			}
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			info, err := cc.Client.API(ctx, "files.info", map[string]any{"file": fileID})
			if err != nil {
				return err
			}
			fileRec, _ := info["file"].(map[string]any)
			summary := render.ToFileSummary(fileRec)
			if summary == nil {
				return agenterrors.New("files.info returned no file", agenterrors.FixableByAgent)
			}

			downloads := slack.DownloadMessageFiles(ctx, cc.Client,
				[]render.MessageSummary{{Files: []render.FileSummary{*summary}}},
				messageDownloadOptions(globals, cc))
			result, ok := downloads[summary.ID]
			if !ok {
				return agenterrors.New("file has no downloadable URL", agenterrors.FixableByAgent)
			}
			payload := map[string]any{
				"id":       summary.ID,
				"name":     summary.Name,
				"title":    summary.Title,
				"mimetype": summary.Mimetype,
				"mode":     summary.Mode,
				"path":     result.Path,
			}
			if !result.OK {
				payload["error"] = result.Error
			}
			return printSingle(globals, payload)
		},
	}
	fileCmd.AddCommand(downloadCmd)
}
