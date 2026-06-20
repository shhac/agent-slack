package cli

import (
	"context"
	"io"
	"strings"

	libcli "github.com/shhac/lib-agent-cli/cli"
	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/auth"
	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/dialog"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/output"
)

type GlobalFlags struct {
	libcli.Globals // Format, TimeoutMS, Debug

	Workspace    string
	Full         bool
	BaseURL      string
	NoCache      bool
	RefreshCache bool
	CacheTTL     string

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
	root := libcli.NewRoot(libcli.Options{
		Use:           "agent-slack",
		Short:         "Slack CLI for AI agents",
		Version:       deps.version,
		Globals:       &globals.Globals,
		DefaultFormat: output.FormatNDJSON,
		UnknownHint:   "run 'agent-slack usage' for full documentation",
	})

	// NewRoot binds --format/--timeout/--debug, silences cobra's own
	// usage/error printing, validates --format up front, and installs the
	// unknown-command handler. We extend its PersistentPreRunE to also wire the
	// stdout/stderr seams tests inject via SetOut/SetErr; cobra only runs the
	// nearest PersistentPreRunE, so subcommands must not define their own.
	innerPreRun := root.PersistentPreRunE
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		globals.stdout = cmd.OutOrStdout()
		globals.stderr = cmd.ErrOrStderr()
		return innerPreRun(cmd, args)
	}

	root.PersistentFlags().StringVarP(&globals.Workspace, "workspace", "w", "", "Workspace URL or unique substring to disambiguate multi-workspace credentials")
	root.PersistentFlags().BoolVar(&globals.Full, "full", false, "Return fuller API payloads where supported")
	root.PersistentFlags().StringVar(&globals.BaseURL, "base-url", "", "Override the Slack API base URL (testing)")
	_ = root.PersistentFlags().MarkHidden("base-url")
	root.PersistentFlags().BoolVar(&globals.NoCache, "no-cache", false, "Bypass the resolution cache entirely (no read, no write)")
	root.PersistentFlags().BoolVar(&globals.RefreshCache, "refresh-cache", false, "Ignore cached reads but still write fresh entries")
	root.PersistentFlags().StringVar(&globals.CacheTTL, "cache-ttl", "", "Override every cache TTL (e.g. 30m, 2h, 0 to disable reads)")

	_ = root.RegisterFlagCompletionFunc("format", fixedCompletions("json", "yaml", "jsonl"))
	registerWorkspaceCompletion(root, globals)

	registerUsage(root)
	registerAuth(root, globals)
	registerMessage(root, globals)
	registerChannel(root, globals)
	registerUser(root, globals)
	registerUsergroup(root, globals)
	registerEmoji(root, globals)
	registerSearch(root, globals)
	registerUnreads(root, globals)
	registerLater(root, globals)
	registerCanvas(root, globals)
	registerFile(root, globals)
	registerWorkflow(root, globals)
	registerCache(root, globals)
	registerConfig(root, globals)
	registerAPI(root, globals)
	attachDomainUsage(root)

	return root
}

// Run builds the root command and hands it to libcli.Run, the family's single
// sink: it executes, renders any bubbled error once via the structured contract
// on stderr, and exits non-zero on failure. Tests use execute() instead, which
// returns the error and writes to the command's own stderr seam.
func Run(version string) {
	libcli.Run(newRootCmd(version))
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
