// Package settings is agent-slack's persistent configuration file — distinct
// from credentials. It holds a flat map of dotted keys to string values
// (currently the cache TTLs) at ~/.config/app.paulie.agent-slack/config.json,
// so a setting persists across shells instead of needing an env var each time.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shhac/lib-agent-cli/xdg"
)

// configDirName deliberately uses a reverse-DNS name rather than the family's
// plain tool name, because the TS stablyai-agent-slack already owns
// ~/.config/agent-slack — a shared dir would mean two writers (see
// internal/credential for the same rationale).
const configDirName = "app.paulie.agent-slack"

// Config is the on-disk settings (version 1).
type Config struct {
	Version  int               `json:"version"`
	Settings map[string]string `json:"settings"`
}

// CacheTTLCategories are the cache TTL knobs, as the suffix after "cache.ttl.".
var CacheTTLCategories = []string{
	"users", "channels", "channel-names", "handles", "dm-channels",
	"workflow-list", "workflow-preview", "workflow-schema",
	"get", "list",
}

// KnownKeys are the settable config keys.
func KnownKeys() []string {
	keys := make([]string, 0, len(CacheTTLCategories))
	for _, c := range CacheTTLCategories {
		keys = append(keys, "cache.ttl."+c)
	}
	return keys
}

// Path is the config file location: $AGENT_SLACK_CONFIG, else
// $XDG_CONFIG_HOME/app.paulie.agent-slack/config.json, else under ~/.config.
func Path() (string, error) {
	if env := os.Getenv("AGENT_SLACK_CONFIG"); env != "" {
		return env, nil
	}
	return filepath.Join(xdg.ConfigDir(configDirName), "config.json"), nil
}

// Load reads the settings, returning an empty (usable) config when the file is
// absent or unreadable.
func Load() (*Config, error) {
	cfg := &Config{Version: 1, Settings: map[string]string{}}
	path, err := Path()
	if err != nil {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if json.Unmarshal(raw, cfg) != nil || cfg.Settings == nil {
		return &Config{Version: 1, Settings: map[string]string{}}, nil
	}
	cfg.Version = 1
	return cfg, nil
}

func (c *Config) save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	c.Version = 1
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Set validates and persists one key/value. cache.ttl.* values must parse as a
// Go duration (or "0").
func Set(key, value string) error {
	if !slices.Contains(KnownKeys(), key) {
		return fmt.Errorf("unknown config key %q; valid: %s", key, strings.Join(KnownKeys(), ", "))
	}
	if value != "0" {
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("invalid duration %q for %s (e.g. 30m, 2h, 0)", value, key)
		}
	}
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.Settings[key] = value
	return cfg.save()
}

// Unset removes a key (no error if absent).
func Unset(key string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	delete(cfg.Settings, key)
	return cfg.save()
}

// Get returns the stored value for key, or "" if unset.
func (c *Config) Get(key string) string { return c.Settings[key] }

// CacheTTLOverrides returns the set cache.ttl.* values keyed by category.
func (c *Config) CacheTTLOverrides() map[string]string {
	out := map[string]string{}
	for k, v := range c.Settings {
		if cat, ok := strings.CutPrefix(k, "cache.ttl."); ok {
			out[cat] = v
		}
	}
	return out
}

// Sorted returns the set key/value pairs in key order.
func (c *Config) Sorted() [][2]string {
	keys := make([]string, 0, len(c.Settings))
	for k := range c.Settings {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([][2]string, len(keys))
	for i, k := range keys {
		out[i] = [2]string{k, c.Settings[k]}
	}
	return out
}
