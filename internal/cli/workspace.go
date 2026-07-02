package cli

// Workspace selector resolution helpers: turning a credential-store resolution
// failure into a structured, agent-actionable error, and the strict host match
// that gates whether env-var credentials may serve a request.

import (
	"net/url"
	"strings"

	"github.com/shhac/agent-slack/internal/credential"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

func mapWorkspaceResolveError(store *credential.Store, selector string, err error) error {
	var ambiguous *credential.AmbiguousSelectorError
	if agenterrors.As(err, &ambiguous) {
		return agenterrors.Newf(agenterrors.FixableByAgent,
			"--workspace %q matches multiple workspaces: %s", selector, strings.Join(ambiguous.Matches, ", ")).
			WithHint("pass a more specific --workspace selector")
	}

	labels := storedWorkspaceLabels(store)
	if len(labels) == 0 {
		return agenterrors.New("no Slack credentials configured", agenterrors.FixableByHuman).
			WithHint(noCredentialsHint)
	}
	if selector == "" {
		return agenterrors.Wrap(err, agenterrors.FixableByHuman).WithHint(noCredentialsHint)
	}
	return agenterrors.Newf(agenterrors.FixableByAgent,
		"no workspace matches %q; configured: %s", selector, strings.Join(labels, ", ")).
		WithHint("pass one of the configured aliases via --workspace, or import the missing workspace")
}

// storedWorkspaceLabels lists each configured credential set as
// "alias (url)" for error messages and hints.
func storedWorkspaceLabels(store *credential.Store) []string {
	creds, err := store.Load()
	if err != nil {
		return nil
	}
	labels := make([]string, 0, len(creds.Workspaces))
	for _, ws := range creds.Workspaces {
		labels = append(labels, ws.Alias+" ("+ws.URL+")")
	}
	return labels
}

// workspaceMatches compares two workspace references by exact host. It is
// deliberately stricter and simpler than the credential store's selector
// matching (no substring/name/team-domain forms): it only guards whether
// env-var credentials may serve a request, where a fuzzy match could hand the
// wrong workspace's token to a permalink. Don't unify it with Store.Resolve.
func workspaceMatches(a, b string) bool {
	ha, hb := workspaceHost(a), workspaceHost(b)
	if ha == "" || hb == "" {
		return false
	}
	return ha == hb
}

func workspaceHost(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimSuffix(s, "/")
}
