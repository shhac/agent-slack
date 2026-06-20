// Package dialog delegates the native secret-entry boilerplate to
// lib-agent-cli/dialog; this thin wrapper keeps agent-slack's existing
// PromptSecret signature (with an initial value to edit) and translates the
// library's neutral error categories into agent-slack's error envelope.
// (Migration shim.)
package dialog

import (
	"context"

	clidialog "github.com/shhac/lib-agent-cli/dialog"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// PromptSecret opens a masked native prompt seeded with initial, so a token
// never transits argv or the agent's conversation. A failure is returned as a
// classified agent-slack error (see Classify).
func PromptSecret(ctx context.Context, title, label, initial string) (string, error) {
	res, err := clidialog.Prompt(ctx, clidialog.Spec{
		Title: title,
		Items: []clidialog.Field{{ID: "secret", Label: label, InputType: clidialog.Password, Initial: initial}},
	})
	if err != nil {
		return "", Classify(err)
	}
	for _, r := range res {
		if r.ID == "secret" {
			return r.Value, nil
		}
	}
	return "", nil
}

// Classify maps a lib-agent-cli/dialog error onto agent-slack's error
// envelope: a user-cancelled dialog is retryable, a headless/unsupported host
// is human-fixable, anything else is agent-fixable. The library's neutral
// errors carry no fixable_by themselves, so the boundary owns that mapping.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	cat, hint := clidialog.ClassifyError(err)
	wrapped := agenterrors.Wrap(err, fixableFor(cat))
	if hint != "" {
		wrapped = wrapped.WithHint(hint)
	}
	return wrapped
}

func fixableFor(cat clidialog.Category) agenterrors.FixableBy {
	switch cat {
	case clidialog.CategoryRetry:
		return agenterrors.FixableByRetry
	case clidialog.CategoryHuman:
		return agenterrors.FixableByHuman
	default:
		return agenterrors.FixableByAgent
	}
}
