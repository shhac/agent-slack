package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/shhac/agent-slack/internal/credential"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/output"
	"github.com/shhac/agent-slack/internal/slack"
)

const noCredentialsHint = "run 'agent-slack auth import-desktop' (or auth add / auth parse-curl)"

// clientContext is everything a command needs to talk to one workspace.
type clientContext struct {
	Client       *slack.Client
	WorkspaceURL string
	AuthType     slack.AuthType
}

// getClient resolves credentials for the --workspace selector (or default).
func getClient(globals *GlobalFlags) (*clientContext, error) {
	return getClientForWorkspace(globals, "")
}

// getClientForWorkspace resolves credentials for a specific workspace URL —
// used when a permalink target names the workspace, which then overrides
// --workspace. Resolution order: env (SLACK_TOKEN…) when it matches the
// request, then the credential store.
func getClientForWorkspace(globals *GlobalFlags, workspaceURL string) (*clientContext, error) {
	selector := strings.TrimSpace(workspaceURL)
	if selector == "" {
		selector = strings.TrimSpace(globals.Workspace)
	}

	if envCtx := clientFromEnv(globals, selector); envCtx != nil {
		return envCtx, nil
	}

	store, err := globals.newStore()
	if err != nil {
		return nil, err
	}
	// With several workspaces and no chosen default, picking one silently
	// risks acting on the wrong Slack — refuse rather than guess.
	if selector == "" {
		if creds, lerr := store.Load(); lerr == nil && len(creds.Workspaces) > 1 && creds.DefaultWorkspaceURL == "" {
			return nil, agenterrors.New("multiple workspaces configured and no default set", agenterrors.FixableByAgent).
				WithHint("pass --workspace <url-or-substring>, or set a default with 'agent-slack auth set-default <url>'")
		}
	}
	ws, err := store.Resolve(selector)
	if err != nil {
		return nil, mapWorkspaceResolveError(store, selector, err)
	}

	if err := healMissingSecrets(globals, store, ws); err != nil {
		return nil, err
	}

	slackAuth := slackAuthFromCred(ws)
	opts := clientOptions(globals)
	if ws.Auth.Type == credential.AuthBrowser {
		opts = append(opts, slack.WithAuthRefresh(desktopRefresh(globals, store, ws.URL)))
	}

	return &clientContext{
		Client:       slack.New(slackAuth, opts...),
		WorkspaceURL: ws.URL,
		AuthType:     slackAuth.Type,
	}, nil
}

// healMissingSecrets handles a stored secret that is a dangling "__KEYCHAIN__"
// placeholder (e.g. seeded by the legacy-file migration and never refilled).
// The literal placeholder must never reach Slack: browser auth gets the same
// Slack Desktop self-heal an expired token would (filling ws in place);
// anything else needs a human. Returns nil when there is nothing to heal.
func healMissingSecrets(globals *GlobalFlags, store *credential.Store, ws *credential.Workspace) error {
	missing := credential.MissingSecrets(*ws)
	if len(missing) == 0 {
		return nil
	}
	if ws.Auth.Type == credential.AuthBrowser {
		if auth, ok := desktopRefresh(globals, store, ws.URL)(context.Background()); ok {
			ws.Auth.XOXC, ws.Auth.XOXD = auth.XOXC, auth.XOXD
			return nil
		}
	}
	return agenterrors.Newf(agenterrors.FixableByHuman,
		"stored credentials for %s are missing %s (no Keychain entry behind the placeholder)",
		ws.URL, strings.Join(missing, ", ")).
		WithHint(fmt.Sprintf("re-run 'agent-slack auth import-desktop', or 'agent-slack auth add --workspace-url %s --form'", ws.URL))
}

// slackAuthFromCred translates a stored credential workspace into the slack
// client's auth shape.
func slackAuthFromCred(ws *credential.Workspace) slack.Auth {
	slackAuth := slack.Auth{WorkspaceURL: ws.URL}
	switch ws.Auth.Type {
	case credential.AuthBrowser:
		slackAuth.Type = slack.AuthBrowser
		slackAuth.XOXC = ws.Auth.XOXC
		slackAuth.XOXD = ws.Auth.XOXD
	default:
		slackAuth.Type = slack.AuthStandard
		slackAuth.Token = ws.Auth.Token
	}
	return slackAuth
}

// clientFromEnv builds a client from SLACK_TOKEN/SLACK_COOKIE_D/
// SLACK_WORKSPACE_URL when set and compatible with the requested workspace.
// Env credentials never auto-refresh (mirrors the TS behavior: the caller
// owns them).
func clientFromEnv(globals *GlobalFlags, selector string) *clientContext {
	token := strings.TrimSpace(os.Getenv("SLACK_TOKEN"))
	if token == "" {
		return nil
	}
	envWorkspace := strings.TrimSpace(os.Getenv("SLACK_WORKSPACE_URL"))
	if selector != "" && !workspaceMatches(envWorkspace, selector) {
		return nil
	}

	var slackAuth slack.Auth
	if strings.HasPrefix(token, "xoxc-") {
		cookie := strings.TrimSpace(os.Getenv("SLACK_COOKIE_D"))
		if cookie == "" || envWorkspace == "" {
			return nil // incomplete browser-auth env; fall through to the store
		}
		slackAuth = slack.Auth{Type: slack.AuthBrowser, XOXC: token, XOXD: cookie, WorkspaceURL: envWorkspace}
	} else {
		slackAuth = slack.Auth{Type: slack.AuthStandard, Token: token, WorkspaceURL: envWorkspace}
	}

	return &clientContext{
		Client:       slack.New(slackAuth, clientOptions(globals)...),
		WorkspaceURL: envWorkspace,
		AuthType:     slackAuth.Type,
	}
}

func clientOptions(globals *GlobalFlags) []slack.Option {
	opts := []slack.Option{slack.WithUserAgent("agent-slack/" + globals.version)}
	if globals.Timeout > 0 {
		opts = append(opts, slack.WithDoer(&http.Client{Timeout: time.Duration(globals.Timeout) * time.Millisecond}))
	}
	if globals.Debug {
		opts = append(opts, slack.WithDebug(globals.stderr))
	}
	if globals.BaseURL != "" {
		opts = append(opts, slack.WithBaseURL(globals.BaseURL))
	}
	opts = append(opts, slack.WithCache(buildCache(globals)))
	opts = append(opts, slack.WithRateLimitNotice(rateLimitNotice(globals)))
	return opts
}

// rateLimitNotice surfaces Slack 429s on stderr as structured notices so an
// agent (or human) sees why a command stalled or failed. The terminal hit adds
// a hint about Slack's 1 req/min non-Marketplace tier on conversations.history
// / conversations.replies — the most common reason reads get throttled.
func rateLimitNotice(globals *GlobalFlags) slack.RateLimitFunc {
	return func(n slack.RateLimitNotice) {
		if n.WillRetry {
			output.WriteNotice(globals.stderr,
				fmt.Sprintf("rate limited by Slack on %s; waiting %s before retry (attempt %d)", n.Method, n.Delay, n.Attempt),
				"")
			return
		}
		output.WriteNotice(globals.stderr,
			fmt.Sprintf("rate limited by Slack on %s; gave up after %d attempts", n.Method, n.Attempt),
			"if this persists on conversations.history/replies, your token is likely on Slack's 1 req/min non-Marketplace tier — use an internal/custom app token to get the 50 req/min tier")
	}
}

// desktopRefresh re-extracts credentials from Slack Desktop when a call hits
// an auth error — xoxc tokens rotate, and this turns the #1 failure mode into
// self-healing. Only workspaces already configured are refreshed.
func desktopRefresh(globals *GlobalFlags, store *credential.Store, workspaceURL string) slack.RefreshFunc {
	return func(ctx context.Context) (slack.Auth, bool) {
		extracted, err := globals.desktopExtract()
		if err != nil {
			return slack.Auth{}, false
		}
		for _, team := range extracted.Teams {
			if !workspaceMatches(team.URL, workspaceURL) {
				continue
			}
			_, _ = store.Upsert(credential.Workspace{ // best-effort persist for the next run
				URL:  team.URL,
				Name: team.Name,
				Auth: credential.Auth{Type: credential.AuthBrowser, XOXC: team.Token, XOXD: extracted.CookieD},
			})
			emitNotice(globals, "credentials refreshed from Slack Desktop", "")
			return slack.Auth{
				Type:         slack.AuthBrowser,
				XOXC:         team.Token,
				XOXD:         extracted.CookieD,
				WorkspaceURL: workspaceURL,
			}, true
		}
		return slack.Auth{}, false
	}
}

func mapWorkspaceResolveError(store *credential.Store, selector string, err error) error {
	var ambiguous *credential.AmbiguousSelectorError
	if agenterrors.As(err, &ambiguous) {
		return agenterrors.Newf(agenterrors.FixableByAgent,
			"--workspace %q matches multiple workspaces: %s", selector, strings.Join(ambiguous.Matches, ", ")).
			WithHint("pass a more specific --workspace selector")
	}

	urls := storedWorkspaceURLs(store)
	if len(urls) == 0 {
		return agenterrors.New("no Slack credentials configured", agenterrors.FixableByHuman).
			WithHint(noCredentialsHint)
	}
	if selector == "" {
		return agenterrors.Wrap(err, agenterrors.FixableByHuman).WithHint(noCredentialsHint)
	}
	return agenterrors.Newf(agenterrors.FixableByAgent,
		"no workspace matches %q; configured: %s", selector, strings.Join(urls, ", ")).
		WithHint("pass one of the configured workspaces via --workspace, or import the missing one")
}

func storedWorkspaceURLs(store *credential.Store) []string {
	creds, err := store.Load()
	if err != nil {
		return nil
	}
	urls := make([]string, 0, len(creds.Workspaces))
	for _, ws := range creds.Workspaces {
		urls = append(urls, ws.URL)
	}
	return urls
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

// requireYes gates destructive mutations: without --yes the command returns a
// human-fixable error describing exactly what would happen.
func requireYes(yes bool, wouldDo string) error {
	if yes {
		return nil
	}
	return agenterrors.Newf(agenterrors.FixableByHuman, "confirmation required: %s", wouldDo).
		WithHint("re-run the same command with --yes to proceed")
}
