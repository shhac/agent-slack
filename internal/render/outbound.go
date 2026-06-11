package render

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// Already-well-formed Slack tokens, protected from entity escaping:
	// <@U…>, <#C…>, <!subteam^S…>, <!here>-style, and <http…|label> links.
	outboundProtectedRe = regexp.MustCompile(
		`<(?:@[UWB][A-Z0-9]+(\|[^>]*)?|#[CG][A-Z0-9]+(\|[^>]*)?|!subteam\^[A-Z0-9]+(\|[^>]*)?|![a-zA-Z]+(\|[^>]*)?|(https?://|mailto:)[^>]+)>`)
	outboundBareUserRe      = regexp.MustCompile(`(^|[^A-Za-z0-9_])@([UWB][A-Z0-9]{6,})\b`)
	outboundBareBroadcastRe = regexp.MustCompile(`(^|[^A-Za-z0-9_])@(here|channel|everyone)\b`)
	outboundStashRe         = regexp.MustCompile("\x00(\\d+)\x00")

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

	// Stash well-formed tokens behind NUL sentinels so their < > | survive
	// the escaping pass.
	var stash []string
	out := outboundProtectedRe.ReplaceAllStringFunc(text, func(m string) string {
		stash = append(stash, m)
		return "\x00" + strconv.Itoa(len(stash)-1) + "\x00"
	})

	out = outboundEscaper.Replace(out)
	out = outboundBareUserRe.ReplaceAllString(out, "$1<@$2>")
	out = outboundBareBroadcastRe.ReplaceAllString(out, "$1<!$2>")

	return outboundStashRe.ReplaceAllStringFunc(out, func(m string) string {
		idx, err := strconv.Atoi(m[1 : len(m)-1])
		if err != nil || idx >= len(stash) {
			return m
		}
		return stash[idx]
	})
}
