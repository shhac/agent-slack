package cli

import "testing"

func TestParseResolveMode(t *testing.T) {
	valid := map[string]resolveMode{
		"":       resolveNone, // unset defaults to none
		"none":   resolveNone,
		"cached": resolveCached,
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

	// resolve/forceRefresh derive cleanly — the old "refresh implies resolve" is
	// structural now, and "refresh but don't resolve" is unrepresentable.
	if resolveNone.resolve() || resolveNone.forceRefresh() {
		t.Error("none must neither resolve nor refresh")
	}
	if !resolveCached.resolve() || resolveCached.forceRefresh() {
		t.Error("cached must resolve without refreshing")
	}
	if !resolveFresh.resolve() || !resolveFresh.forceRefresh() {
		t.Error("fresh must resolve and refresh")
	}
}
