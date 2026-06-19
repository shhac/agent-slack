// Package dialog delegates the native secret-entry boilerplate to
// lib-agent-cli/dialog; this thin wrapper keeps agent-slack's existing
// PromptSecret signature (with an initial value to edit). (Migration shim.)
package dialog

import (
	"context"

	clidialog "github.com/shhac/lib-agent-cli/dialog"
)

// PromptSecret opens a masked native prompt seeded with initial, so a token
// never transits argv or the agent's conversation. It returns a structured
// error on a headless host.
func PromptSecret(ctx context.Context, title, label, initial string) (string, error) {
	res, err := clidialog.Prompt(ctx, clidialog.Spec{
		Title:  title,
		Fields: []clidialog.Field{{ID: "secret", Label: label, Hidden: true, Initial: initial}},
	})
	if err != nil {
		return "", err
	}
	return res["secret"], nil
}
