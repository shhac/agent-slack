package cli

import (
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/output"
)

type GlobalFlags struct {
	Workspace string
	Format    string
	Timeout   int
	Debug     bool
	Full      bool
}

func newRootCmd(version string) *cobra.Command {
	globals := &GlobalFlags{}
	root := &cobra.Command{
		Use:           "agent-slack",
		Short:         "Slack CLI for AI agents",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&globals.Workspace, "workspace", "w", "", "Workspace URL or unique substring to disambiguate multi-workspace credentials")
	root.PersistentFlags().StringVarP(&globals.Format, "format", "f", "", "Output format: json, yaml, jsonl")
	root.PersistentFlags().IntVarP(&globals.Timeout, "timeout", "t", 0, "Request timeout in milliseconds")
	root.PersistentFlags().BoolVarP(&globals.Debug, "debug", "d", false, "Log redacted HTTP debug records to stderr")
	root.PersistentFlags().BoolVar(&globals.Full, "full", false, "Return fuller API payloads where supported")

	registerUsage(root)

	return root
}

func Execute(version string) error {
	return execute(newRootCmd(version))
}

func execute(root *cobra.Command) error {
	err := root.Execute()
	if err != nil {
		output.WriteError(output.Stderr(), err)
	}
	return err
}
