package render

import (
	"regexp"
	"strings"
)

var (
	// Already-well-formed Slack tokens, protected from entity escaping:
	// <@U…>, <#C…>, <!subteam^S…>, <!here>-style, and <http…|label> links.
	outboundProtectedRe = regexp.MustCompile(
		`<(?:@[UWB][A-Z0-9]+(\|[^>]*)?|#[CG][A-Z0-9]+(\|[^>]*)?|!subteam\^[A-Z0-9]+(\|[^>]*)?|![a-zA-Z]+(\|[^>]*)?|(https?://|mailto:)[^>]+)>`)
	outboundBareUserRe      = regexp.MustCompile(`(^|[^A-Za-z0-9_])@([UWB][A-Z0-9]{6,})\b`)
	outboundBareBroadcastRe = regexp.MustCompile(`(^|[^A-Za-z0-9_])@(here|channel|everyone)\b`)

	outboundEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
)

// FormatOutboundText prepares user-authored text for chat.postMessage /
// chat.update: literal & < > are escaped per Slack's mrkdwn contract, and
// bare @U123 / @here mentions are promoted to real <@U123> / <!here> tokens.
// Already-well-formed Slack tokens pass through untouched.
func FormatOutboundText(text string) string {
	if text == "" {
		return ""
	}

	// Protect well-formed tokens so their < > | survive the escaping pass.
	out, restore := Protect(text, outboundProtectedRe)
	out = outboundEscaper.Replace(out)
	out = outboundBareUserRe.ReplaceAllString(out, "$1<@$2>")
	out = outboundBareBroadcastRe.ReplaceAllString(out, "$1<!$2>")
	return restore(out)
}
