package cli

import "testing"

func TestParseUserMode(t *testing.T) {
	valid := map[string]userMode{
		"":       usersNone, // unset defaults to none
		"none":   usersNone,
		"cached": usersCached,
		"fresh":  usersFresh,
	}
	for in, want := range valid {
		got, err := parseUserMode(in)
		if err != nil || got != want {
			t.Errorf("parseUserMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := parseUserMode("bogus"); err == nil {
		t.Error("parseUserMode(\"bogus\") should error")
	}

	// resolve/forceRefresh derive cleanly — the old "refresh implies resolve" is
	// structural now, and "refresh but don't resolve" is unrepresentable.
	if usersNone.resolve() || usersNone.forceRefresh() {
		t.Error("none must neither resolve nor refresh")
	}
	if !usersCached.resolve() || usersCached.forceRefresh() {
		t.Error("cached must resolve without refreshing")
	}
	if !usersFresh.resolve() || !usersFresh.forceRefresh() {
		t.Error("fresh must resolve and refresh")
	}
}
