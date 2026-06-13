package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/settings"
)

func TestResolveCacheTTLSources(t *testing.T) {
	t.Setenv("AGENT_SLACK_CONFIG", filepath.Join(t.TempDir(), "config.json"))

	// Defaults include the new serve TTLs.
	if d := resolveCacheTTL(&GlobalFlags{}); d.Get != 5*time.Minute || d.List != 5*time.Minute {
		t.Fatalf("serve defaults: get=%v list=%v", d.Get, d.List)
	}

	// Config file is the lowest override above defaults.
	if err := settings.Set("cache.ttl.channels", "30m"); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set("cache.ttl.get", "2m"); err != nil {
		t.Fatal(err)
	}
	if d := resolveCacheTTL(&GlobalFlags{}); d.Channels != 30*time.Minute || d.Get != 2*time.Minute {
		t.Errorf("config overrides not applied: %+v", d)
	}

	// Per-category env beats the config file.
	t.Setenv("AGENT_SLACK_CACHE_TTL_CHANNELS", "10m")
	if d := resolveCacheTTL(&GlobalFlags{}); d.Channels != 10*time.Minute {
		t.Errorf("env should beat config: channels=%v", d.Channels)
	}

	// The --cache-ttl flag beats everything, for all categories.
	if d := resolveCacheTTL(&GlobalFlags{CacheTTL: "1m"}); d.Channels != time.Minute || d.Get != time.Minute || d.Users != time.Minute {
		t.Errorf("flag should override all: %+v", d)
	}
}

func TestResolveCacheTTLPrecedence(t *testing.T) {
	// Built-in defaults when nothing is set.
	def := resolveCacheTTL(&GlobalFlags{})
	if def.Users != 24*time.Hour || def.Channels != time.Hour {
		t.Fatalf("defaults wrong: %+v", def)
	}

	// Global flag overrides every category.
	all := resolveCacheTTL(&GlobalFlags{CacheTTL: "30m"})
	if all.Users != 30*time.Minute || all.WorkflowSchema != 30*time.Minute {
		t.Errorf("global --cache-ttl did not apply to all: %+v", all)
	}

	// Per-category env beats the global override.
	t.Setenv("AGENT_SLACK_CACHE_TTL", "30m")
	t.Setenv("AGENT_SLACK_CACHE_TTL_CHANNELS", "5m")
	mixed := resolveCacheTTL(&GlobalFlags{})
	if mixed.Channels != 5*time.Minute {
		t.Errorf("per-category env should win: channels=%v", mixed.Channels)
	}
	if mixed.Users != 30*time.Minute {
		t.Errorf("global env should apply where no per-category set: users=%v", mixed.Users)
	}
}

func TestParseTTL(t *testing.T) {
	cases := map[string]struct {
		want time.Duration
		ok   bool
	}{
		"":      {0, false},
		"  ":    {0, false},
		"0":     {0, true}, // valid: disables reads
		"45m":   {45 * time.Minute, true},
		"2h":    {2 * time.Hour, true},
		"-1h":   {0, false}, // negative rejected
		"bogus": {0, false},
	}
	for in, want := range cases {
		got, ok := parseTTL(in)
		if got != want.want || ok != want.ok {
			t.Errorf("parseTTL(%q) = (%v,%v), want (%v,%v)", in, got, ok, want.want, want.ok)
		}
	}
}
