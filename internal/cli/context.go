package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shhac/agent-slack/internal/credential"
	agenterrors "github.com/shhac/agent-slack/internal/errors"
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

	// A secret can be a dangling "__KEYCHAIN__" placeholder (e.g. seeded by
	// the legacy-file migration and never refilled). Never send the literal
	// placeholder to Slack: browser auth gets the same Slack Desktop self-heal
	// an expired token would; anything else needs a human.
	if missing := credential.MissingSecrets(*ws); len(missing) > 0 {
		healed := false
		if ws.Auth.Type == credential.AuthBrowser {
			if auth, ok := desktopRefresh(globals, store, ws.URL)(context.Background()); ok {
				ws.Auth.XOXC, ws.Auth.XOXD = auth.XOXC, auth.XOXD
				healed = true
			}
		}
		if !healed {
			return nil, agenterrors.Newf(agenterrors.FixableByHuman,
				"stored credentials for %s are missing %s (no Keychain entry behind the placeholder)",
				ws.URL, strings.Join(missing, ", ")).
				WithHint(fmt.Sprintf("re-run 'agent-slack auth import-desktop', or 'agent-slack auth add --workspace-url %s --form'", ws.URL))
		}
	}

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
	return opts
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
			_, _ = fmt.Fprintln(globals.stderr, "agent-slack: credentials refreshed from Slack Desktop")
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

// workspaceMatches compares two workspace identifiers by host (URL forms) or
// case-insensitive equality.
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

// appCacheDir is where downloads and the user cache live. XDG_CACHE_HOME is
// the right home for both: they are re-derivable copies (downloads re-fetch
// by immutable file ID, the user cache has a 24h TTL), safe to purge —
// unlike XDG_RUNTIME_DIR (size-limited tmpfs, cleared per session) or
// XDG_DATA_HOME (data the app owns). Named like the config dir —
// app.paulie.agent-slack — to stay clear of the TS tool's paths.
func appCacheDir() string {
	const dirName = "app.paulie.agent-slack"
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, dirName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Shared tmp: suffix the UID so another user can't pre-own the path.
		return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", dirName, os.Getuid()))
	}
	return filepath.Join(home, ".cache", dirName)
}

func downloadsDir() string {
	return filepath.Join(appCacheDir(), "downloads")
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
