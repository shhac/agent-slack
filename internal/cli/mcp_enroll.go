// Browser credential enrollment for MCP principals: the descriptor rendered
// by lib-agent-mcp's approval flow, and the callback that validates a
// person's pasted Slack credentials, stores them under their principal's
// alias, and returns the workspace binding. See lib-agent-mcp
// design-docs/enrollment.md for the trust model; the invariants here are
// (1) writes are scoped to the verified principal's alias, and (2) a slot
// converges on one Slack identity — tokens resolving to a different
// team/user are an error, never a silent re-point.
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/shhac/lib-agent-mcp/oauth"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/slack"
)

// mcpEnrollmentDescriptor is the one-mode "smart token" form: the token's own
// prefix decides the auth shape, so nobody has to know Slack's token
// taxonomy up front. The cookie and workspace URL matter only for browser
// (xoxc) tokens, and the callback says so when they're missing.
func mcpEnrollmentDescriptor() oauth.CredentialDescriptor {
	return oauth.CredentialDescriptor{
		Title: "Connect your Slack workspace",
		Intro: "One-time setup. Your credentials are stored on the server operator's machine and used only for your own calls.",
		Modes: []oauth.CredentialMode{{
			Key: "slack",
			Fields: []oauth.CredentialField{
				{Key: "token", Label: "Slack token", Secret: true,
					Help: "Paste whichever you have: a browser token (xoxc-…) or an API token (xoxp-…/xoxb-…). " +
						"To grab a browser token, open your Slack tab, then devtools → Console and run this (it logs each " +
						"workspace's URL and its xoxc token — paste the token here, the URL below):",
					Snippet: "Object.values(JSON.parse(localStorage.localConfig_v2).teams).forEach(t => console.log(t.url, t.token))"},
				{Key: "xoxd", Label: "Session cookie", Secret: true, Optional: true,
					Help: "Only needed with a browser token (xoxc). The console can't read this one (it's HttpOnly), so grab it from " +
						"devtools → Application → Cookies → the \"d\" cookie (xoxd-…)."},
				{Key: "workspace_url", Label: "Workspace URL", Optional: true,
					Help: "Only needed with a browser token, e.g. https://acme.slack.com."},
			},
		}},
	}
}

// resolveEnrollAuth classifies a submitted token by its prefix into the
// credential auth shape, then derives the transport auth from it via the
// canonical slackAuthFromCred converter — one encoding, so the two can't drift.
// A browser token (xoxc-) additionally requires the session cookie and
// workspace URL; the returned error is human-facing form text. Pure (no I/O),
// so the token-shape branch is unit-testable in isolation.
func resolveEnrollAuth(token, cookie, wsURL string) (slack.Auth, credential.Auth, error) {
	var credAuth credential.Auth
	if strings.HasPrefix(token, "xoxc-") {
		if cookie == "" {
			return slack.Auth{}, credential.Auth{}, errors.New(
				`this is a browser token (xoxc-…), so the "d" session cookie is also needed: devtools → Application → Cookies → d`)
		}
		if wsURL == "" {
			return slack.Auth{}, credential.Auth{}, errors.New(
				"a browser token also needs the workspace URL, e.g. https://acme.slack.com")
		}
		credAuth = credential.Auth{Type: credential.AuthBrowser, XOXC: token, XOXD: cookie}
	} else {
		credAuth = credential.Auth{Type: credential.AuthStandard, Token: token}
	}
	return slackAuthFromCred(&credential.Workspace{URL: wsURL, Auth: credAuth}), credAuth, nil
}

// mcpEnroll validates submitted credentials against auth.test and stores them
// under alias = principal name. Errors are human-facing form text, not the
// CLI's structured stderr shape.
func mcpEnroll(globals *GlobalFlags) oauth.EnrollFunc {
	return func(ctx context.Context, req oauth.EnrollRequest) (oauth.EnrollResult, error) {
		token := strings.TrimSpace(req.Values["token"])
		cookie := strings.TrimSpace(req.Values["xoxd"])
		wsURL := strings.TrimRight(strings.TrimSpace(req.Values["workspace_url"]), "/")

		slackAuth, credAuth, err := resolveEnrollAuth(token, cookie, wsURL)
		if err != nil {
			return oauth.EnrollResult{}, err
		}

		resp, err := slack.New(slackAuth, baseClientOptions(globals)...).API(ctx, "auth.test", nil)
		if err != nil {
			return oauth.EnrollResult{}, fmt.Errorf("slack rejected these credentials: %v", err)
		}
		teamID, _ := resp["team_id"].(string)
		userID, _ := resp["user_id"].(string)
		teamName, _ := resp["team"].(string)
		if wsURL == "" {
			respURL, _ := resp["url"].(string)
			wsURL = strings.TrimRight(strings.TrimSpace(respURL), "/")
		}
		if teamID == "" || userID == "" || wsURL == "" {
			return oauth.EnrollResult{}, errors.New(
				"slack accepted the credentials but did not return a full identity — enter the workspace URL explicitly and try again")
		}

		store, err := globals.newStore()
		if err != nil {
			return oauth.EnrollResult{}, errors.New("the server could not open its credential store — tell the operator")
		}
		if err := checkIdentityConvergence(store, req.Principal, teamID, userID); err != nil {
			return oauth.EnrollResult{}, err
		}
		if _, err := store.Upsert(credential.Workspace{
			Alias:  req.Principal,
			URL:    wsURL,
			Name:   teamName,
			TeamID: teamID,
			UserID: userID,
			Auth:   credAuth,
		}); err != nil {
			return oauth.EnrollResult{}, fmt.Errorf("storing the credentials failed: %v", err)
		}
		return oauth.EnrollResult{Binding: map[string]string{bindingKeyWorkspace: req.Principal}}, nil
	}
}

// checkIdentityConvergence enforces the one-slot-one-identity rule: strictly
// by alias (never URL/name matching — a principal name must not resolve into
// someone else's workspace record), and only when the stored slot already
// knows who it is.
func checkIdentityConvergence(store *credential.Store, alias, teamID, userID string) error {
	creds, err := store.Load()
	if err != nil {
		return errors.New("the server could not read its credential store — tell the operator")
	}
	for i := range creds.Workspaces {
		w := &creds.Workspaces[i]
		if w.Alias != alias || w.TeamID == "" || w.UserID == "" {
			continue
		}
		if w.TeamID != teamID || w.UserID != userID {
			return fmt.Errorf(
				"these credentials belong to a different Slack account than the one enrolled for %q — if that change is intended, ask the operator to re-point your identity (pair add %s --bind workspace=…)",
				alias, alias)
		}
	}
	return nil
}
