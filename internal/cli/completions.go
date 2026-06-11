package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/slack"
)

// maxTargetCompletions caps how many cached candidates a <target> completion
// returns, so a large workspace never floods the shell.
const maxTargetCompletions = 50

// targetCompletion completes a <target> argument (first positional) from the
// per-workspace cache: channel names and seen user IDs, most-recently-used
// first. Cache-only and read-only — never hits the API, so it stays instant.
func targetCompletion(globals *GlobalFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 { // only the first positional is a target
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		items := slack.ReadTargetCompletions(appCacheDir(), completionWorkspaceURL(globals), toComplete, maxTargetCompletions)
		out := make([]string, 0, len(items))
		for _, it := range items {
			if it.Description != "" {
				out = append(out, it.Value+"\t"+it.Description) // tab => zsh/fish description
			} else {
				out = append(out, it.Value)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
}

// completionWorkspaceURL picks the workspace whose cache to read for
// completions: the --workspace selector if it matches a configured workspace,
// else the stored default. Best-effort and read-only.
func completionWorkspaceURL(globals *GlobalFlags) string {
	store, err := globals.newStore()
	if err != nil {
		return ""
	}
	creds, err := store.Load()
	if err != nil {
		return ""
	}
	if sel := strings.TrimSpace(globals.Workspace); sel != "" {
		for _, w := range creds.Workspaces {
			if strings.Contains(strings.ToLower(w.URL), strings.ToLower(sel)) ||
				strings.EqualFold(w.Name, sel) {
				return w.URL
			}
		}
	}
	return creds.DefaultWorkspaceURL
}

// fixedCompletions completes a flag from a closed set of values (no file
// completion fallback).
func fixedCompletions(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// registerWorkspaceCompletion completes --workspace from the configured
// workspace URLs (read-only; no API).
func registerWorkspaceCompletion(cmd *cobra.Command, globals *GlobalFlags) {
	_ = cmd.RegisterFlagCompletionFunc("workspace",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			store, err := globals.newStore()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			creds, err := store.Load()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			urls := make([]string, 0, len(creds.Workspaces))
			for _, w := range creds.Workspaces {
				urls = append(urls, w.URL)
			}
			return urls, cobra.ShellCompDirectiveNoFileComp
		})
}
