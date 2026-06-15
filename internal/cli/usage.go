package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func registerUsage(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "LLM-optimized usage overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), usageText)
			return err
		},
	}
	parent.AddCommand(cmd)
}

// attachDomainUsage adds a `usage` subcommand to each domain that has a
// detail page. Called after all domains are registered.
func attachDomainUsage(root *cobra.Command) {
	for _, sub := range root.Commands() {
		text, ok := domainUsage[sub.Name()]
		if !ok {
			continue
		}
		sub.AddCommand(&cobra.Command{
			Use:   "usage",
			Short: "Detailed " + sub.Name() + " documentation for LLMs",
			RunE: func(cmd *cobra.Command, args []string) error {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), text)
				return err
			},
		})
	}
}
