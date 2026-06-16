package cli

import (
	"testing"

	"github.com/shhac/agent-slack/internal/slack"
)

func TestParseResolveMode(t *testing.T) {
	valid := map[string]resolveMode{
		"":       resolveAuto, // unset defaults to auto (resolution on)
		"none":   resolveNone,
		"cached": resolveCached,
		"auto":   resolveAuto,
		"fresh":  resolveFresh,
	}
	for in, want := range valid {
		got, err := parseResolveMode(in)
		if err != nil || got != want {
			t.Errorf("parseResolveMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := parseResolveMode("bogus"); err == nil {
		t.Error("parseResolveMode(\"bogus\") should error")
	}

	// resolve() — only none (and unset) means "don't resolve".
	if resolveNone.resolve() {
		t.Error("none must not resolve")
	}
	for _, m := range []resolveMode{resolveCached, resolveAuto, resolveFresh} {
		if !m.resolve() {
			t.Errorf("%q must resolve", m)
		}
	}

	// policy() maps to the slack cache policy.
	cases := map[resolveMode]slack.ResolvePolicy{
		resolveCached: slack.ResolveCacheOnly,
		resolveAuto:   slack.ResolveCacheThenFetch,
		resolveFresh:  slack.ResolveBypassCache,
	}
	for m, want := range cases {
		if got := m.policy(); got != want {
			t.Errorf("%q.policy() = %v, want %v", m, got, want)
		}
	}
}
