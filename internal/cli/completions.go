package cli

import "github.com/spf13/cobra"

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
