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

	"github.com/shhac/agent-slack/internal/slack"
)

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

// resolveCacheTTL builds the per-category TTL: built-in defaults, overridden by
// a global --cache-ttl / AGENT_SLACK_CACHE_TTL, then by per-category
// AGENT_SLACK_CACHE_TTL_<CATEGORY> env vars (most specific wins).
func resolveCacheTTL(globals *GlobalFlags) slack.CacheTTL {
	ttl := slack.DefaultCacheTTL()
	global := globals.CacheTTL
	if global == "" {
		global = os.Getenv("AGENT_SLACK_CACHE_TTL")
	}
	if d, ok := parseTTL(global); ok {
		ttl = slack.CacheTTL{Users: d, Channels: d, ChannelNames: d, Handles: d, WorkflowPreview: d, WorkflowSchema: d}
	}
	for env, field := range map[string]*time.Duration{
		"AGENT_SLACK_CACHE_TTL_USERS":            &ttl.Users,
		"AGENT_SLACK_CACHE_TTL_CHANNELS":         &ttl.Channels,
		"AGENT_SLACK_CACHE_TTL_CHANNEL_NAMES":    &ttl.ChannelNames,
		"AGENT_SLACK_CACHE_TTL_HANDLES":          &ttl.Handles,
		"AGENT_SLACK_CACHE_TTL_WORKFLOW_PREVIEW": &ttl.WorkflowPreview,
		"AGENT_SLACK_CACHE_TTL_WORKFLOW_SCHEMA":  &ttl.WorkflowSchema,
	} {
		if d, ok := parseTTL(os.Getenv(env)); ok {
			*field = d
		}
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
