package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/slack"
)

// maxCompletions caps how many cached candidates a completion returns, so a
// large workspace never floods the shell.
const maxCompletions = 50

type compFunc = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)

// cacheCompletion builds a completion that draws from the per-workspace cache
// for the given sources. firstArgOnly stops after one positional (a <target>);
// false completes every positional (e.g. dm-open's user list). Cache-only and
// read-only — never hits the API, so it stays instant and is empty on a cold
// cache rather than falling back to filenames.
func cacheCompletion(globals *GlobalFlags, sources slack.CompletionSource, firstArgOnly bool) compFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if firstArgOnly && len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		items := slack.ReadCompletions(appCacheDir(), completionWorkspaceURL(globals), toComplete, maxCompletions, sources)
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

// targetCompletion completes a <target> (channel or DM user) first positional.
func targetCompletion(globals *GlobalFlags) compFunc {
	return cacheCompletion(globals, slack.CompleteChannels|slack.CompleteUsers, true)
}

// channelArgCompletion completes a channel-only first positional.
func channelArgCompletion(globals *GlobalFlags) compFunc {
	return cacheCompletion(globals, slack.CompleteChannels, true)
}

// triggerArgCompletion completes an Ft… trigger id from the cache.
func triggerArgCompletion(globals *GlobalFlags) compFunc {
	return cacheCompletion(globals, slack.CompleteTriggers, true)
}

// scheduledArgCompletion completes a scheduled-message id from the cache (warmed
// by `scheduled list`).
func scheduledArgCompletion(globals *GlobalFlags) compFunc {
	return cacheCompletion(globals, slack.CompleteScheduled, true)
}

// draftArgCompletion completes a draft id (Dr…, warmed by `draft list`) or a
// target — get/edit/delete/send accept either.
func draftArgCompletion(globals *GlobalFlags) compFunc {
	return cacheCompletion(globals, slack.CompleteChannels|slack.CompleteUsers|slack.CompleteDrafts, true)
}

// userArgsCompletion completes a user on every positional (dm-open takes many).
func userArgsCompletion(globals *GlobalFlags) compFunc {
	return cacheCompletion(globals, slack.CompleteUsers, false)
}

// usergroupArgsCompletion completes a usergroup on every positional (warmed by
// `usergroup list` / mention resolution).
func usergroupArgsCompletion(globals *GlobalFlags) compFunc {
	return cacheCompletion(globals, slack.CompleteUsergroups, false)
}

// usergroupArgCompletion completes a usergroup-only first positional.
func usergroupArgCompletion(globals *GlobalFlags) compFunc {
	return cacheCompletion(globals, slack.CompleteUsergroups, true)
}

// registerFlagCompletion attaches a cache-backed completion to a flag value.
func registerFlagCompletion(cmd *cobra.Command, flag string, globals *GlobalFlags, sources slack.CompletionSource) {
	_ = cmd.RegisterFlagCompletionFunc(flag, cacheCompletion(globals, sources, false))
}

// completionWorkspaceURL picks the workspace whose cache to read for
// completions, via the same credential resolver every command uses (URL,
// host, name, team-domain, or unique-substring matching; "" means the stored
// default). Best-effort: any resolution failure just means no suggestions.
func completionWorkspaceURL(globals *GlobalFlags) string {
	store, err := globals.newStore()
	if err != nil {
		return ""
	}
	ws, err := store.Resolve(globals.Workspace)
	if err != nil {
		return ""
	}
	return ws.URL
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
