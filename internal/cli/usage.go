package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/output"
)

const usageText = `agent-slack: Slack CLI for AI agents.

STATUS: scaffold. The command surface is being ported from the TypeScript
agent-slack (see design-docs/initial-design.md for the full plan).

Planned domains:
  auth       Credential import and verification (browser xoxc/xoxd or bot tokens)
  message    get | list | send | edit | delete | react | scheduled
  channel    list | new | invite
  user       list | get
  search     all | messages | files
  workflow   list | preview | get | run
  canvas     get
  unreads    Unread messages across channels, DMs, and threads
  later      Saved-for-later message management

Output contract:
  Lists default to NDJSON (one JSON object per line); single resources to JSON.
  Errors are JSON on stderr with fixable_by: agent | human | retry, plus a hint.
  Tokens and secrets are never printed.

Safety:
  Mutations (send, edit, delete, invite, run) require --yes; that gate is the
  human-in-the-loop control.
`

func registerUsage(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "LLM-optimized usage overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(output.Stdout(), usageText)
			return err
		},
	}
	parent.AddCommand(cmd)
}
