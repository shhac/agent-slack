package cli

import (
	"testing"
	"time"
)

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
