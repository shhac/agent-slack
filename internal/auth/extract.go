// Package auth extracts Slack browser credentials (xoxc tokens + the xoxd
// cookie) from local sources: Slack Desktop, Chrome, Brave, Firefox, and pasted
// cURL commands. The pure parsing/decryption logic lives in its own files and
// is unit-tested; the platform-specific file/keychain/AppleScript access is
// kept thin and isolated.
package auth

// Team is one Slack workspace recovered from a local source.
type Team struct {
	URL   string `json:"url"`
	Name  string `json:"name,omitempty"`
	Token string `json:"token"`
}

// Extracted is the result of importing from one source: a shared xoxd cookie
// plus one or more workspace tokens.
type Extracted struct {
	CookieD string            `json:"cookie_d"`
	Teams   []Team            `json:"teams"`
	Source  map[string]string `json:"source,omitempty"`
}
