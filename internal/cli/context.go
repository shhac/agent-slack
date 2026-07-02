package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shhac/agent-slack/internal/credential"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/slack"
)

const noCredentialsHint = "run 'agent-slack auth import-desktop' (or auth add / auth parse-curl)"

// clientContext is everything a command needs to talk to one workspace.
type clientContext struct {
	Client *slack.Client
	// Alias is the stored credential set serving this client ("" for env-var
	// credentials, which live outside the store).
	Alias        string
	WorkspaceURL string
	AuthType     slack.AuthType
	// CacheKey is the resolved <team_id>/<user_id> identity namespace for this
	// workspace ("" when the identity could not be resolved). Commands that touch
	// the cache subtree directly — downloads, emoji images — scope by it.
	CacheKey string
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
		if creds, lerr := store.Load(); lerr == nil && len(creds.Workspaces) > 1 && creds.DefaultWorkspace == "" {
			return nil, agenterrors.New("multiple workspaces configured and no default set", agenterrors.FixableByAgent).
				WithHint("pass --workspace <alias-or-url>, or set a default with 'agent-slack auth set-default <alias>'")
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

	key := resolveIdentityKey(store, ws, baseClientOptions(globals), slackAuth)

	opts := clientOptions(globals, key)
	if ws.Auth.Type == credential.AuthBrowser {
		opts = append(opts, slack.WithAuthRefresh(desktopRefresh(globals, store, ws.Alias, ws.URL)))
	}

	return &clientContext{
		Client:       slack.New(slackAuth, opts...),
		Alias:        ws.Alias,
		WorkspaceURL: ws.URL,
		AuthType:     slackAuth.Type,
		CacheKey:     key,
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
		if auth, ok := desktopRefresh(globals, store, ws.Alias, ws.URL)(context.Background()); ok {
			ws.Auth.XOXC, ws.Auth.XOXD = auth.XOXC, auth.XOXD
			return nil
		}
	}
	return agenterrors.Newf(agenterrors.FixableByHuman,
		"stored credentials for %s (%s) are missing %s (no Keychain entry behind the placeholder)",
		ws.Alias, ws.URL, strings.Join(missing, ", ")).
		WithHint(fmt.Sprintf("re-run 'agent-slack auth import-desktop', or 'agent-slack auth add --alias %s --workspace-url %s --form'", ws.Alias, ws.URL))
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

	// Env credentials persist nothing, so the identity (and its cache key) is
	// resolved fresh each invocation. Inert on failure, like the stored path.
	key := slack.IdentityCacheKey(bootstrapIdentity(baseClientOptions(globals), slackAuth))

	return &clientContext{
		Client:       slack.New(slackAuth, clientOptions(globals, key)...),
		WorkspaceURL: envWorkspace,
		AuthType:     slackAuth.Type,
		CacheKey:     key,
	}
}

// clientOptions is baseClientOptions plus the identity-scoped resolution cache.
func clientOptions(globals *GlobalFlags, key string) []slack.Option {
	opts := baseClientOptions(globals)
	opts = append(opts, slack.WithCache(buildCache(globals, key)))
	return opts
}

// baseClientOptions builds every client option except the cache: user agent,
// timeout, debug, base URL, and the rate-limit notice. The cache is added
// separately because the identity bootstrap needs a cache-less client.
func baseClientOptions(globals *GlobalFlags) []slack.Option {
	opts := []slack.Option{slack.WithUserAgent("agent-slack/" + globals.version)}
	if globals.TimeoutMS > 0 {
		opts = append(opts, slack.WithDoer(&http.Client{Timeout: time.Duration(globals.TimeoutMS) * time.Millisecond}))
	}
	if globals.Debug {
		opts = append(opts, slack.WithDebug(globals.stderr))
	}
	if globals.BaseURL != "" {
		opts = append(opts, slack.WithBaseURL(globals.BaseURL))
	}
	opts = append(opts, slack.WithRateLimitNotice(rateLimitNotice(globals)))
	return opts
}

// rateLimitNotice surfaces Slack 429s on stderr as structured notices so an
// agent (or human) sees why a command stalled or failed. The terminal hit adds
// a hint about Slack's 1 req/min non-Marketplace tier on conversations.history
// / conversations.replies — the most common reason reads get throttled.
func rateLimitNotice(globals *GlobalFlags) slack.RateLimitFunc {
	const tierHint = "if this persists on conversations.history/replies, your token is likely on Slack's 1 req/min non-Marketplace tier — use an internal/custom app token to get the 50 req/min tier"
	return func(n slack.RateLimitNotice) {
		if !n.WillRetry {
			emitNotice(globals,
				fmt.Sprintf("rate limited by Slack on %s; gave up after %d attempts", n.Method, n.Attempt),
				tierHint)
			return
		}
		msg := fmt.Sprintf("rate limited by Slack on %s; waiting %s before retry (attempt %d)", n.Method, n.Delay, n.Attempt)
		// When Slack asks for longer than our cap, say so — otherwise a user who
		// waited the reported time is surprised by an immediate re-throttle.
		if n.RetryAfter > n.Delay {
			msg += fmt.Sprintf(" (Slack asked for %s, capped to %s)", n.RetryAfter, n.Delay)
		}
		emitNotice(globals, msg, "")
	}
}

// desktopRefresh re-extracts credentials from Slack Desktop when a call hits
// an auth error — xoxc tokens rotate, and this turns the #1 failure mode into
// self-healing. Only workspaces already configured are refreshed, and the
// persist targets the alias this refresher was built for — never a URL match,
// which could hit another alias holding the same workspace URL.
func desktopRefresh(globals *GlobalFlags, store *credential.Store, alias, workspaceURL string) slack.RefreshFunc {
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
				Alias: alias,
				URL:   team.URL,
				Name:  team.Name,
				Auth:  credential.Auth{Type: credential.AuthBrowser, XOXC: team.Token, XOXD: extracted.CookieD},
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

// requireYes gates destructive mutations: without --yes the command returns a
// human-fixable error describing exactly what would happen.
func requireYes(yes bool, wouldDo string) error {
	if yes {
		return nil
	}
	return agenterrors.Newf(agenterrors.FixableByHuman, "confirmation required: %s", wouldDo).
		WithHint("re-run the same command with --yes to proceed")
}
