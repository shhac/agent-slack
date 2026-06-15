package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/output"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerCache(parent *cobra.Command, globals *GlobalFlags) {
	cacheCmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and clear the local resolution cache",
	}
	parent.AddCommand(cacheCmd)
	handleUnknownSubcommand(cacheCmd)
	registerCacheInfo(cacheCmd, globals)
	registerCacheWarm(cacheCmd, globals)
	registerCachePurge(cacheCmd, globals)
}

func registerCacheWarm(parent *cobra.Command, globals *GlobalFlags) {
	var pageDelay time.Duration
	var includeBots bool
	cmd := &cobra.Command{
		Use:       "warm [users|channels|usergroups...]",
		Short:     "Pre-fetch users, channels, and usergroups into the cache (paced for rate limits; streams JSONL progress)",
		Long:      "Pre-fetch list endpoints into the cache. With no arguments all categories are warmed; pass one or more of users, channels, usergroups to scope it.",
		Args:      cobra.OnlyValidArgs,
		ValidArgs: []string{slack.WarmUsers, slack.WarmChannels, slack.WarmUsergroups},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			w := output.NewNDJSONWriter(globals.stdout)
			return slack.WarmWorkspace(cmd.Context(), cc.Client, slack.WarmOptions{
				PageDelay:   pageDelay,
				IncludeBots: includeBots,
				Categories:  args,
			}, func(e slack.WarmEvent) {
				_ = w.WriteItem(e) // stream progress as we go; consumers can filter done:true for the summary
			})
		},
	}
	cmd.Flags().DurationVar(&pageDelay, "page-delay", time.Second, "Pause between paged API calls to stay under Slack rate limits (0 to disable)")
	cmd.Flags().BoolVar(&includeBots, "include-bots", false, "Include bot users")
	parent.AddCommand(cmd)
}

// cacheWorkspaceLabels maps each present cache subdir key to its configured
// workspace URL (or a clearly-unknown label for orphaned dirs).
func cacheWorkspaceLabels(globals *GlobalFlags, keys []string) map[string]string {
	labels := map[string]string{}
	for _, k := range keys {
		labels[k] = "unknown (" + k + ")"
	}
	store, err := globals.newStore()
	if err != nil {
		return labels
	}
	creds, err := store.Load()
	if err != nil {
		return labels
	}
	for _, w := range creds.Workspaces {
		if key := slack.WorkspaceCacheKey(w.URL); labels[key] != "" {
			labels[key] = w.URL
		}
	}
	return labels
}

func registerCacheInfo(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show what's cached per workspace (entries, size, age); all workspaces unless --workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := appCacheDir()

			var keys []string
			if sel := globals.Workspace; sel != "" {
				if url := completionWorkspaceURL(globals); url != "" {
					keys = []string{slack.WorkspaceCacheKey(url)}
				}
			} else {
				all, err := slack.CachedWorkspaceKeys(dir)
				if err != nil {
					return err
				}
				keys = all
			}

			labels := cacheWorkspaceLabels(globals, keys)
			now := time.Now().UnixMilli()
			var total int64
			workspaces := make([]map[string]any, 0, len(keys))
			for _, key := range keys {
				cats, err := slack.InspectCacheDir(dir, key)
				if err != nil {
					return err
				}
				var wsBytes int64
				out := make([]map[string]any, 0, len(cats))
				for _, c := range cats {
					wsBytes += c.Bytes
					entry := map[string]any{"category": c.Category, "entries": c.Entries, "bytes": c.Bytes}
					if c.NewestMS > 0 {
						entry["newest_age_seconds"] = (now - c.NewestMS) / 1000
						entry["oldest_age_seconds"] = (now - c.OldestMS) / 1000
					}
					out = append(out, entry)
				}
				total += wsBytes
				workspaces = append(workspaces, map[string]any{
					"workspace": labels[key], "cache_key": key, "bytes": wsBytes, "categories": out,
				})
			}

			return printSingle(globals, map[string]any{
				"cache_dir":       dir,
				"downloads_bytes": dirBytes(filepath.Join(dir, "downloads")),
				"total_bytes":     total,
				"workspaces":      workspaces,
			})
		},
	}
	parent.AddCommand(cmd)
}

func registerCachePurge(parent *cobra.Command, globals *GlobalFlags) {
	var allWorkspaces, downloads bool
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Delete cached data: the workspace's resolution cache by default; --all-workspaces and/or --downloads. Local + regenerable.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := appCacheDir()
			result := map[string]any{}

			if downloads {
				if err := os.RemoveAll(downloadsDir()); err != nil {
					return err
				}
				result["downloads"] = "cleared"
			}

			// Purge a resolution cache unless --downloads was the sole target.
			downloadsOnly := downloads && !allWorkspaces && globals.Workspace == ""
			switch {
			case downloadsOnly:
				// nothing more
			case allWorkspaces:
				cleared, err := slack.PurgeAllCaches(dir)
				if err != nil {
					return err
				}
				result["cleared_workspaces"] = cleared
			default:
				url := completionWorkspaceURL(globals)
				if url == "" {
					return agenterrors.New("no workspace to purge", agenterrors.FixableByAgent).
						WithHint("pass --workspace <selector>, --all-workspaces, or --downloads")
				}
				if err := slack.PurgeCacheDir(dir, slack.WorkspaceCacheKey(url)); err != nil {
					return err
				}
				result["purged"] = url
			}
			return printSingle(globals, result)
		},
	}
	cmd.Flags().BoolVar(&allWorkspaces, "all-workspaces", false, "Clear every workspace's resolution cache")
	cmd.Flags().BoolVar(&downloads, "downloads", false, "Clear the downloaded-files cache (not workspace-scoped)")
	parent.AddCommand(cmd)
}

// dirBytes sums the sizes of files under dir (0 if absent). Best-effort.
func dirBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
