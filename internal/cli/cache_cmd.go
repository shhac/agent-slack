package cli

import (
	"io/fs"
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
	var noBots, staleOnly bool
	cmd := &cobra.Command{
		Use:       "warm [users|channels|usergroups|emoji|dm-channels...]",
		Short:     "Pre-fetch users, channels, usergroups, custom emoji, and open-DM ids into the cache (paced for rate limits; streams JSONL progress)",
		Long:      "Pre-fetch list endpoints into the cache. With no arguments all categories are warmed; pass one or more of users, channels, usergroups, emoji, dm-channels to scope it. dm-channels reads the already-open DM list (it never opens a new DM).",
		Args:      cobra.OnlyValidArgs,
		ValidArgs: []string{slack.WarmUsers, slack.WarmChannels, slack.WarmUsergroups, slack.WarmEmoji, slack.WarmDMChannels},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			w := output.NewNDJSONWriter(globals.stdout)
			return slack.WarmWorkspace(cmd.Context(), cc.Client, slack.WarmOptions{
				PageDelay:  pageDelay,
				NoBots:     noBots,
				StaleOnly:  staleOnly,
				Categories: args,
			}, func(e slack.WarmEvent) {
				_ = w.WriteItem(e) // stream progress as we go; consumers can filter done:true for the summary
			})
		},
	}
	cmd.Flags().DurationVar(&pageDelay, "page-delay", time.Second, "Pause between paged API calls to stay under Slack rate limits (0 to disable)")
	cmd.Flags().BoolVar(&noBots, "no-bots", false, "Exclude bot users (by default bots are warmed so the set is complete for resolution; excluding them leaves the completeness sentinel un-armed)")
	cmd.Flags().BoolVar(&staleOnly, "stale-only", false, "Skip categories still complete within their sentinel window (re-warm only what has gone stale)")
	parent.AddCommand(cmd)
}

// cacheWorkspaceLabels maps each present identity-cache key (<team_id>/<user_id>)
// to its configured workspace URL (or a clearly-unknown label for orphaned dirs,
// e.g. a credential since removed).
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
		if key := slack.IdentityCacheKey(w.TeamID, w.UserID); key != "" && labels[key] != "" {
			labels[key] = w.URL
		}
	}
	return labels
}

func registerCacheInfo(parent *cobra.Command, globals *GlobalFlags) {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show what's cached per identity (entries, size, age); all identities unless --workspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := appCacheDir()

			var keys []string
			if globals.Workspace != "" {
				if key, _ := selectedIdentityKey(globals); key != "" {
					keys = []string{key}
				}
			} else {
				all, err := slack.CachedIdentityKeys(dir)
				if err != nil {
					return err
				}
				keys = all
			}

			labels := cacheWorkspaceLabels(globals, keys)
			now := time.Now().UnixMilli()
			var total, downloads int64
			workspaces := make([]map[string]any, 0, len(keys))
			for _, key := range keys {
				cats, err := slack.InspectCacheDir(dir, key)
				if err != nil {
					return err
				}
				ws, wsBytes := cacheWorkspacePayload(labels[key], key, cats, now)
				dlBytes := dirBytes(downloadsDir(key))
				ws["downloads_bytes"] = dlBytes
				total += wsBytes
				downloads += dlBytes
				workspaces = append(workspaces, ws)
			}

			return printSingle(globals, map[string]any{
				"cache_dir":       dir,
				"downloads_bytes": downloads,
				"total_bytes":     total,
				"workspaces":      workspaces,
			})
		},
	}
	parent.AddCommand(cmd)
}

// cacheWorkspacePayload shapes one workspace's cache report and returns it with
// its byte total — the pure transform (byte sums, age-second math) extracted
// from registerCacheInfo's I/O loop so it can be exercised without the cache dir.
func cacheWorkspacePayload(label, key string, cats []slack.CacheCategory, now int64) (map[string]any, int64) {
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
	return map[string]any{
		"workspace": label, "cache_key": key, "bytes": wsBytes, "categories": out,
	}, wsBytes
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
				if err := purgeDownloads(globals, dir, allWorkspaces, result); err != nil {
					return err
				}
			}

			// Also purge the resolution cache — except when --downloads was the
			// sole target (a bare --downloads with no scope clears only downloads).
			purgeResolution := !downloads || allWorkspaces || globals.Workspace != ""
			if purgeResolution {
				if err := purgeResolutionCache(globals, dir, allWorkspaces, result); err != nil {
					return err
				}
			}
			return printSingle(globals, result)
		},
	}
	cmd.Flags().BoolVar(&allWorkspaces, "all-workspaces", false, "Clear every identity's resolution cache")
	cmd.Flags().BoolVar(&downloads, "downloads", false, "Also clear the resolved identity's downloaded files (kept by a plain purge)")
	parent.AddCommand(cmd)
}

// purgeResolutionCache clears the resolution cache for every identity
// (--all-workspaces) or the resolved one, recording what it cleared.
func purgeResolutionCache(globals *GlobalFlags, dir string, allWorkspaces bool, result map[string]any) error {
	if allWorkspaces {
		cleared, err := slack.PurgeAllCaches(dir)
		if err != nil {
			return err
		}
		result["cleared_workspaces"] = cleared
		return nil
	}
	key, url, err := requireSelectedIdentity(globals)
	if err != nil {
		return err
	}
	if err := slack.PurgeCacheDir(dir, key); err != nil {
		return err
	}
	result["purged"] = url
	return nil
}

// purgeDownloads clears downloaded files for every cached identity
// (--all-workspaces) or the resolved one.
func purgeDownloads(globals *GlobalFlags, dir string, allWorkspaces bool, result map[string]any) error {
	if allWorkspaces {
		if err := slack.PurgeAllDownloads(dir); err != nil {
			return err
		}
	} else {
		key, _, err := requireSelectedIdentity(globals)
		if err != nil {
			return err
		}
		if err := slack.PurgeDownloads(dir, key); err != nil {
			return err
		}
	}
	result["downloads"] = "cleared"
	return nil
}

// requireSelectedIdentity resolves the --workspace selector (or default) to its
// identity key, returning a structured error when no workspace resolves or its
// identity is unresolved — the single guard the purge paths share.
func requireSelectedIdentity(globals *GlobalFlags) (key, url string, err error) {
	key, url = selectedIdentityKey(globals)
	if key == "" {
		return "", url, errNoWorkspaceToPurge(url)
	}
	return key, url, nil
}

// errNoWorkspaceToPurge distinguishes "no workspace selected" from "workspace
// known but its identity is unresolved" (nothing has been cached for it yet).
func errNoWorkspaceToPurge(url string) error {
	if url != "" {
		return agenterrors.Newf(agenterrors.FixableByAgent, "workspace %s has no resolved identity yet; nothing is cached to purge", url).
			WithHint("run any command (or 'agent-slack auth test') against it first to resolve its identity")
	}
	return agenterrors.New("no workspace to purge", agenterrors.FixableByAgent).
		WithHint("pass --workspace <selector>, --all-workspaces, or --downloads")
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
