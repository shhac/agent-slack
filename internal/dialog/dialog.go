// Package dialog prompts the human for secrets via a native OS dialog, so a
// token can be typed or pasted without ever passing through the agent's
// conversation or shell history. Matches the --form pattern used across the
// agent-* CLI family.
package dialog

import (
	"context"
	"fmt"

	"github.com/ncruces/zenity"
)

func PromptSecret(ctx context.Context, title, label, initial string) (string, error) {
	value, err := zenity.Entry(
		label,
		zenity.Title(title),
		zenity.EntryText(initial),
		zenity.HideText(),
		zenity.Context(ctx),
	)
	if err != nil {
		return "", fmt.Errorf("prompt for secret: %w", err)
	}
	return value, nil
}
