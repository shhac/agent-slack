package auth

import (
	"errors"
	"net/url"
	"regexp"

	browsercookies "github.com/shhac/lib-agent-browsercookies"
)

// slackCookieTarget is the extraction policy for Slack's session cookie: the
// `d` cookie on any *.slack.com host. The value comes back verbatim; Slack
// applies its own URL-decoding afterward (xoxdFromPlain for the Chromium paths,
// decodeCookieValue for the plaintext Firefox/Safari paths), so decoding stays
// out of the shared library boundary.
var slackCookieTarget = browsercookies.Target{
	CookieName:   "d",
	HostSuffixes: []string{"slack.com"},
}

var xoxdValueRe = regexp.MustCompile(`xoxd-[A-Za-z0-9%/+_=.\-]+`)

// xoxdFromPlain finds the xoxd-* token in a cookie value and returns it
// URL-decoded once. The scan tolerates any leading bytes, so it is robust to a
// value that still carries Chromium's meta-version prefix.
func xoxdFromPlain(plain []byte) (string, error) {
	match := xoxdValueRe.Find(plain)
	if match == nil {
		return "", errors.New("no xoxd-* token in cookie value")
	}
	token := string(match)
	if decoded, derr := url.PathUnescape(token); derr == nil {
		return decoded, nil
	}
	return token, nil
}
