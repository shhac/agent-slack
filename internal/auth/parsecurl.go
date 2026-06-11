package auth

import (
	"net/url"
	"regexp"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

var (
	curlURLRe = regexp.MustCompile(`(?i)curl\s+['"]?(https?://([^.]+)\.slack\.com[^'"\s]*)`)

	cookieFieldRes = []*regexp.Regexp{
		regexp.MustCompile(`(?:-b|--cookie)\s+\$?'([^']+)'`),
		regexp.MustCompile(`(?:-b|--cookie)\s+\$?"([^"]+)"`),
		regexp.MustCompile(`-H\s+\$?'[Cc]ookie:\s*([^']+)'`),
		regexp.MustCompile(`-H\s+\$?"[Cc]ookie:\s*([^"]+)"`),
	}

	xoxdInCookieRe = regexp.MustCompile(`(?:^|;\s*)d=(xoxd-[^;]+)`)

	tokenRes = []*regexp.Regexp{
		regexp.MustCompile(`(?:^|[?&\s])token=(xoxc-[A-Za-z0-9-]+)`),
		regexp.MustCompile(`"token"\s*:\s*"(xoxc-[A-Za-z0-9-]+)"`),
		regexp.MustCompile(`name="token"[^x]*?(xoxc-[A-Za-z0-9-]+)`),
		regexp.MustCompile(`\b(xoxc-[A-Za-z0-9-]+)\b`),
	}
)

// ParseCurl extracts a workspace URL, xoxc token, and xoxd cookie from a Slack
// API request pasted as a cURL command. All "could not find" failures are
// agent-fixable (the input was wrong), not human/secret problems.
func ParseCurl(input string) (Team, string, error) {
	urlMatch := curlURLRe.FindStringSubmatch(input)
	if urlMatch == nil {
		return Team{}, "", agenterrors.New("could not find a Slack workspace URL in the cURL command", agenterrors.FixableByAgent)
	}
	workspaceURL := "https://" + urlMatch[2] + ".slack.com"

	var cookieHeader string
	for _, re := range cookieFieldRes {
		if m := re.FindStringSubmatch(input); m != nil {
			cookieHeader = m[1]
			break
		}
	}
	xoxdMatch := xoxdInCookieRe.FindStringSubmatch(cookieHeader)
	if xoxdMatch == nil {
		return Team{}, "", agenterrors.New("could not find the xoxd cookie (d=xoxd-...) in the cURL command", agenterrors.FixableByAgent)
	}
	xoxd := xoxdMatch[1]
	if decoded, err := url.PathUnescape(xoxd); err == nil {
		xoxd = decoded
	}

	var token string
	for _, re := range tokenRes {
		if m := re.FindStringSubmatch(input); m != nil {
			token = m[1]
			break
		}
	}
	if token == "" {
		return Team{}, "", agenterrors.New("could not find the xoxc token in the cURL command", agenterrors.FixableByAgent)
	}

	return Team{URL: workspaceURL, Token: token}, xoxd, nil
}
