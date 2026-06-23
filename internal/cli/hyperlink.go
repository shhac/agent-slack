package cli

import (
	"github.com/shhac/lib-agent-cli/hyperlink"
)

// linkStyle wraps a hyperlink label in ANSI underline + cyan so a clickable
// label reads as a link even where the terminal doesn't tint OSC 8 itself: SGR
// 4 (underline) + 36 (cyan), reset with 0. The styling sits inside the OSC 8
// wrap so the styled text is exactly the clickable region.
const (
	linkStyleOn  = "\x1b[4;36m"
	linkStyleOff = "\x1b[0m"
)

// hyperlinkEncoder returns the TranscriptOptions.Hyperlink encoder, or nil to
// keep links in plain "label (url)" / markdown form. Gated like the image
// resolver: the --hyperlinks mode (off/auto/on) decides per stream via
// hyperlink.Active — off never, auto on a TTY, on forces. A bad mode degrades
// to nil (the flag is validated up front anyway).
//
// The label is underlined and colored before OSC 8 wrapping so the link is
// visually obvious, not just clickable.
func hyperlinkEncoder(globals *GlobalFlags) func(url, label string) string {
	mode, err := hyperlink.ParseMode(globals.Hyperlinks)
	if err != nil || !hyperlink.Active(globals.stdout, mode) {
		return nil
	}
	return func(url, label string) string {
		return hyperlink.Encode(url, linkStyleOn+label+linkStyleOff)
	}
}
