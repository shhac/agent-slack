package cli

import (
	"testing"

	"github.com/shhac/agent-slack/internal/credential"
)

func TestCompletionWorkspaceURL(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.UpsertMany([]credential.Workspace{
		{URL: "https://acme.slack.com", Name: "Acme", Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-a"}},
		{URL: "https://globex.slack.com", Name: "Globex", Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-g"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetDefault("https://acme.slack.com"); err != nil {
		t.Fatal(err)
	}
	globals := func(selector string) *GlobalFlags {
		return &GlobalFlags{
			Workspace: selector,
			newStore:  func() (*credential.Store, error) { return env.store, nil },
		}
	}

	cases := map[string]string{
		"":                         "https://acme.slack.com", // default
		"globex":                   "https://globex.slack.com",
		"https://globex.slack.com": "https://globex.slack.com",
		"Globex":                   "https://globex.slack.com", // name match via resolver
		"nope-no-match":            "",                         // unknown → no suggestions
		"slack":                    "",                         // ambiguous → no suggestions
	}
	for selector, want := range cases {
		if got := completionWorkspaceURL(globals(selector)); got != want {
			t.Errorf("selector %q: got %q, want %q", selector, got, want)
		}
	}
}
