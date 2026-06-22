package cli

import (
	"github.com/shhac/lib-agent-cli/hyperlink"
)

// hyperlinkEncoder returns the TranscriptOptions.Hyperlink encoder, or nil to
// keep links in plain "label (url)" / markdown form. Gated like the image
// resolver: the --hyperlinks mode (off/auto/on) decides per stream via
// hyperlink.Active — off never, auto on a TTY, on forces. A bad mode degrades
// to nil (the flag is validated up front anyway).
func hyperlinkEncoder(globals *GlobalFlags) func(url, label string) string {
	mode, err := hyperlink.ParseMode(globals.Hyperlinks)
	if err != nil || !hyperlink.Active(globals.stdout, mode) {
		return nil
	}
	return hyperlink.Encode
}
