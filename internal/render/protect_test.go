package render

import (
	"regexp"
	"testing"
)

func TestProtect(t *testing.T) {
	code := regexp.MustCompile("`[^`]+`")

	// A transform between mask and restore leaves protected spans untouched.
	masked, restore := Protect("a `x*y` b", code)
	masked += "*bold*" // a transform that would have mangled the `x*y` span
	got := restore(masked)
	if got != "a `x*y` b*bold*" {
		t.Errorf("got %q", got)
	}

	// No regexps → identity, and restore leaves out-of-range sentinels alone.
	m, r := Protect("plain")
	if m != "plain" || r("plain \x009\x00") != "plain \x009\x00" {
		t.Errorf("identity/oob restore wrong: %q", r("plain \x009\x00"))
	}
}
