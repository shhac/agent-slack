package cli

// Cache and storage configuration: where the per-workspace resolution cache
// and downloads live, and how the global flags/env shape cache behavior.
// The client/credential resolution itself stays in context.go.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shhac/agent-slack/internal/settings"
	"github.com/shhac/agent-slack/internal/slack"
)

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
// flags and environment. This single helper feeds both client constructors.
func buildCache(globals *GlobalFlags) *slack.Cache {
	mode := slack.CacheNormal
	switch {
	case globals.NoCache || os.Getenv("AGENT_SLACK_NO_CACHE") != "":
		mode = slack.CacheOff
	case globals.RefreshCache:
		mode = slack.CacheRefresh
	}
	return slack.NewCache(appCacheDir(), mode, resolveCacheTTL(globals), nil)
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
