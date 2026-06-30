package cli

// Cache and storage configuration: where the per-workspace resolution cache
// and downloads live, and how the global flags/env shape cache behavior.
// The client/credential resolution itself stays in context.go.

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shhac/lib-agent-cli/xdg"

	"github.com/shhac/agent-slack/internal/settings"
	"github.com/shhac/agent-slack/internal/slack"
)

// appName is the reverse-DNS application directory shared by the cache and
// config roots. It deviates from the family's plain-tool-name convention to
// stay clear of the TS stablyai-agent-slack's ~/.config/agent-slack paths.
const appName = "app.paulie.agent-slack"

// ttlFields maps each cache category (the cache.ttl.<cat> suffix) to its field
// in a CacheTTL, so config, env, and flag overrides all drive one table.
func ttlFields(t *slack.CacheTTL) map[string]*time.Duration {
	return map[string]*time.Duration{
		"users":            &t.Users,
		"channels":         &t.Channels,
		"channel-names":    &t.ChannelNames,
		"handles":          &t.Handles,
		"dm-channels":      &t.DMChannels,
		"usergroups":       &t.Usergroups,
		"workflow-list":    &t.WorkflowList,
		"workflow-preview": &t.WorkflowPreview,
		"workflow-schema":  &t.WorkflowSchema,

		"users-complete":      &t.UsersComplete,
		"channels-complete":   &t.ChannelsComplete,
		"usergroups-complete": &t.UsergroupsComplete,

		"get":  &t.Get,
		"list": &t.List,
	}
}

func ttlEnvVar(category string) string {
	return "AGENT_SLACK_CACHE_TTL_" + strings.ToUpper(strings.ReplaceAll(category, "-", "_"))
}

// buildCache assembles the per-invocation resolution cache from the global
// flags and environment. key is the resolved <team_id>/<user_id> identity
// namespace ("" leaves caching inert). This single helper feeds both client
// constructors.
func buildCache(globals *GlobalFlags, key string) *slack.Cache {
	dir := appCacheDir()
	slack.MigrateLegacyLayout(dir) // one-time sweep of pre-identity cache dirs
	mode := slack.CacheNormal
	switch {
	case globals.NoCache || os.Getenv("AGENT_SLACK_NO_CACHE") != "":
		mode = slack.CacheOff
	case globals.RefreshCache:
		mode = slack.CacheRefresh
	}
	return slack.NewCache(dir, key, mode, resolveCacheTTL(globals), nil)
}

// resolveCacheTTL builds the per-category TTL. Precedence, highest first:
// the --cache-ttl flag (all categories), per-category env
// AGENT_SLACK_CACHE_TTL_<CAT>, global env AGENT_SLACK_CACHE_TTL (all),
// the persisted config file (cache.ttl.<cat>), then the built-in defaults.
func resolveCacheTTL(globals *GlobalFlags) slack.CacheTTL {
	ttl := slack.DefaultCacheTTL()
	fields := ttlFields(&ttl)
	setAll := func(d time.Duration) {
		for _, f := range fields {
			*f = d
		}
	}

	// config file (lowest override)
	if cfg, err := settings.Load(); err == nil {
		for cat, raw := range cfg.CacheTTLOverrides() {
			if f, ok := fields[cat]; ok {
				if d, ok := parseTTL(raw); ok {
					*f = d
				}
			}
		}
	}
	// global env, then per-category env
	if d, ok := parseTTL(os.Getenv("AGENT_SLACK_CACHE_TTL")); ok {
		setAll(d)
	}
	for cat, f := range fields {
		if d, ok := parseTTL(os.Getenv(ttlEnvVar(cat))); ok {
			*f = d
		}
	}
	// global flag (highest)
	if d, ok := parseTTL(globals.CacheTTL); ok {
		setAll(d)
	}
	return ttl
}

// parseTTL parses a Go duration; "0" is valid and disables reads for a category.
func parseTTL(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if raw == "0" {
		return 0, true
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

// appCacheDir is the cache root ($XDG_CACHE_HOME/app.paulie.agent-slack, else
// ~/.cache/app.paulie.agent-slack), via lib-agent-cli's xdg helper so the whole
// agent-* family derives roots one way. XDG_CACHE_HOME is the right home: the
// user cache (24h TTL) and downloads (re-fetched by immutable file ID) are
// re-derivable and safe to purge — unlike XDG_RUNTIME_DIR (size-limited tmpfs)
// or XDG_DATA_HOME (data the app owns). Per-identity data lives under
// <root>/<team_id>/<user_id>/.
func appCacheDir() string {
	return xdg.CacheDir(appName)
}

// downloadsDir is where one identity's downloaded files land, beside its
// resolution cache under the same <team_id>/<user_id> subtree. An empty key
// (identity not yet resolved) falls back to the cache root so a download still
// has somewhere to go rather than failing.
func downloadsDir(key string) string {
	return filepath.Join(appCacheDir(), key, "downloads")
}
