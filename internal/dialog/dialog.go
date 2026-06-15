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

// entry is the secret-prompt backend — a package var so tests can swap the
// native OS dialog for a fake and exercise PromptSecret over the boundary.
var entry = zenity.Entry

func PromptSecret(ctx context.Context, title, label, initial string) (string, error) {
	value, err := entry(
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
