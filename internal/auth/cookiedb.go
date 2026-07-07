package auth

import browsercookies "github.com/shhac/lib-agent-browsercookies"

// extractChromiumCookieD reads the Slack `d` cookie from a Chromium-family
// Cookies SQLite database and returns the decoded xoxd- value. queries are the
// macOS Keychain Safe Storage lookups for the decryption password; they are
// injected as the library's Keychain so the shared library drives the
// snapshot → decrypt → host-hash-strip mechanism while Slack keeps its
// account/attribute-aware password lookups.
func extractChromiumCookieD(cookiesPath string, queries []safeStorageQuery, opts ...browsercookies.Option) (string, error) {
	// The default platform runs Slack's real keychain lookups; a test may append
	// WithPlatform to inject a fake one (options apply in order, last wins).
	opts = append([]browsercookies.Option{browsercookies.WithPlatform(slackPlatform(queries))}, opts...)
	res, err := browsercookies.ExtractChromiumStore(
		browsercookies.ChromiumStore{Paths: []string{cookiesPath}},
		slackCookieTarget,
		opts...,
	)
	if err != nil {
		return "", err
	}
	return xoxdFromPlain([]byte(res.Value))
}
