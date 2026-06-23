package cli

import (
	"bytes"
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
	if enc == nil || enc("https://x.com", "label") == "label" {
		t.Error("on mode should produce an OSC 8 encoder that wraps the label")
	}
}
