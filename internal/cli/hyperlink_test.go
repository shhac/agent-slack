package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestHyperlinkEncoderGating — the per-stream gate decides whether OSC 8 escapes
// are emitted. A bytes.Buffer is never a TTY, so off/auto/bad-mode must yield no
// encoder (escapes must never leak into a non-TTY pipe); on forces.
func TestHyperlinkEncoderGating(t *testing.T) {
	cases := []struct {
		mode    string
		wantNil bool
	}{
		{"off", true},
		{"", true},
		{"auto", true}, // non-TTY
		{"bogus", true},
		{"on", false}, // forces past the TTY check
	}
	for _, tc := range cases {
		g := &GlobalFlags{}
		g.Hyperlinks = tc.mode
		g.stdout = &bytes.Buffer{}
		enc := hyperlinkEncoder(g)
		if (enc == nil) != tc.wantNil {
			t.Errorf("hyperlinkEncoder(%q): got nil=%v, want nil=%v", tc.mode, enc == nil, tc.wantNil)
		}
	}

	// on mode produces a working OSC 8 encoder.
	g := &GlobalFlags{}
	g.Hyperlinks = "on"
	g.stdout = &bytes.Buffer{}
	enc := hyperlinkEncoder(g)
	if enc == nil {
		t.Fatal("on mode should produce an OSC 8 encoder")
	}
	got := enc("https://x.com", "label")
	if got == "label" {
		t.Error("on mode should wrap the label in an OSC 8 sequence")
	}
	// The label must carry underline + color so it reads as a link, and the
	// styling must sit inside the OSC 8 wrap (before the label text).
	if !strings.Contains(got, linkStyleOn+"label"+linkStyleOff) {
		t.Errorf("label should be styled (underline + color): %q", got)
	}
	if !strings.HasPrefix(got, "\x1b]8;;https://x.com\x1b\\"+linkStyleOn) {
		t.Errorf("styling should sit inside the OSC 8 wrap: %q", got)
	}
}
