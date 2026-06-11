package cli

import (
	"context"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/auth"
	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/dialog"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/output"
)

type GlobalFlags struct {
	Workspace string
	Format    string
	Timeout   int
	Debug     bool
	Full      bool
	BaseURL   string

	// Injected seams — wired by newRootCmd, substituted by tests. Constructor
	// injection (not package globals) so test roots are hermetic and
	// parallelizable.
	version        string
	newStore       func() (*credential.Store, error)
	desktopExtract func() (*auth.Extracted, error)
	promptSecret   func(ctx context.Context, title, label, initial string) (string, error)
	stdout         io.Writer
	stderr         io.Writer
}

// rootDeps are the production defaults newRootCmd wires; tests build roots
// via newRootCmdWithDeps with fakes.
type rootDeps struct {
	version        string
	newStore       func() (*credential.Store, error)
	desktopExtract func() (*auth.Extracted, error)
	promptSecret   func(ctx context.Context, title, label, initial string) (string, error)
}

func newRootCmd(version string) *cobra.Command {
	return newRootCmdWithDeps(rootDeps{
		version:        version,
		newStore:       credential.New,
		desktopExtract: auth.ExtractFromSlackDesktop,
		promptSecret:   dialog.PromptSecret,
	})
}

func newRootCmdWithDeps(deps rootDeps) *cobra.Command {
	globals := &GlobalFlags{
		version:        deps.version,
		newStore:       deps.newStore,
		desktopExtract: deps.desktopExtract,
		promptSecret:   deps.promptSecret,
	}
	root := &cobra.Command{
		Use:           "agent-slack",
		Short:         "Slack CLI for AI agents",
		Version:       deps.version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Writers resolve from cobra (tests inject via SetOut/SetErr), and
		// --format validates once up front so a bad value can never surface
		// AFTER a mutation has already run. No subcommand may define its own
		// PersistentPreRunE — cobra only runs the nearest one.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			globals.stdout = cmd.OutOrStdout()
			globals.stderr = cmd.ErrOrStderr()
			if globals.Format != "" {
				if _, err := output.ParseFormat(globals.Format); err != nil {
					return err
				}
			}
			return nil
		},
	}

	root.PersistentFlags().StringVarP(&globals.Workspace, "workspace", "w", "", "Workspace URL or unique substring to disambiguate multi-workspace credentials")
	root.PersistentFlags().StringVarP(&globals.Format, "format", "f", "", "Output format: json, yaml, jsonl")
	root.PersistentFlags().IntVarP(&globals.Timeout, "timeout", "t", 0, "Request timeout in milliseconds")
	root.PersistentFlags().BoolVarP(&globals.Debug, "debug", "d", false, "Log redacted HTTP debug records to stderr")
	root.PersistentFlags().BoolVar(&globals.Full, "full", false, "Return fuller API payloads where supported")
	root.PersistentFlags().StringVar(&globals.BaseURL, "base-url", "", "Override the Slack API base URL (testing)")
	_ = root.PersistentFlags().MarkHidden("base-url")

	registerUsage(root)
	registerAuth(root, globals)
	registerMessage(root, globals)
	registerChannel(root, globals)
	registerUser(root, globals)
	registerSearch(root, globals)
	registerUnreads(root, globals)
	registerLater(root, globals)
	registerCanvas(root, globals)
	registerFile(root, globals)
	registerWorkflow(root, globals)
	registerAPI(root, globals)
	attachDomainUsage(root)

	return root
}

func Execute(version string) error {
	return execute(newRootCmd(version))
}

// handleUnknownSubcommand makes a parent command answer unknown subcommands
// with a structured agent-fixable error enumerating the valid ones, instead
// of cobra's default help text on stdout.
func handleUnknownSubcommand(cmd *cobra.Command) {
	cmd.Args = cobra.ArbitraryArgs
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			names := make([]string, 0, len(cmd.Commands()))
			for _, sub := range cmd.Commands() {
				names = append(names, sub.Name())
			}
			return agenterrors.Newf(agenterrors.FixableByAgent,
				"unknown command %q for %q; valid: %s", args[0], cmd.CommandPath(), strings.Join(names, ", ")).
				WithHint("run 'agent-slack usage' for full documentation")
		}
		return cmd.Help()
	}
}

func execute(root *cobra.Command) error {
	err := root.Execute()
	if err != nil {
		output.WriteError(root.ErrOrStderr(), err)
	}
	return err
}
