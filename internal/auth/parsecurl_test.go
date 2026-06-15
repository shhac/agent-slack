package auth

import "testing"

func TestParseCurl(t *testing.T) {
	input := `curl 'https://acme.slack.com/api/conversations.history' \
  -H 'Cookie: d=xoxd-AbCdEf123%2F456; other=1' \
  --data-raw 'token=xoxc-111-222-333&channel=C1'`

	team, xoxd, err := ParseCurl(input)
	if err != nil {
		t.Fatal(err)
	}
	if team.URL != "https://acme.slack.com" {
		t.Errorf("URL = %q", team.URL)
	}
	if team.Token != "xoxc-111-222-333" {
		t.Errorf("token = %q", team.Token)
	}
	if xoxd != "xoxd-AbCdEf123/456" { // %2F decoded to /
		t.Errorf("xoxd = %q, want URL-decoded", xoxd)
	}
}

func TestParseCurlBcookieFlagAndJSONToken(t *testing.T) {
	input := `curl "https://globex.slack.com/api/auth.test" -b "d=xoxd-zzz; foo=bar" -H 'content-type: application/json' --data '{"token":"xoxc-json-tok"}'`
	team, xoxd, err := ParseCurl(input)
	if err != nil {
		t.Fatal(err)
	}
	if team.URL != "https://globex.slack.com" || team.Token != "xoxc-json-tok" || xoxd != "xoxd-zzz" {
		t.Errorf("got %+v xoxd=%q", team, xoxd)
	}
}

// Enterprise-grid (multi-label) hosts resolve to the full host, not the first
// label — the case the old single-label regex failed to match.
func TestParseCurlEnterpriseHost(t *testing.T) {
	input := `curl 'https://acme.enterprise.slack.com/api/auth.test' -b 'd=xoxd-ent' --data 'token=xoxc-ent-tok'`
	team, xoxd, err := ParseCurl(input)
	if err != nil {
		t.Fatal(err)
	}
	if team.URL != "https://acme.enterprise.slack.com" || team.Token != "xoxc-ent-tok" || xoxd != "xoxd-ent" {
		t.Errorf("got %+v xoxd=%q", team, xoxd)
	}
}

func TestParseCurlTokenAndCookieVariants(t *testing.T) {
	cases := map[string]struct {
		input     string
		wantURL   string
		wantToken string
		wantXOXD  string
	}{
		"token in query string, Cookie header": {
			input:     `curl 'https://acme.slack.com/api/x?token=xoxc-qs-tok&foo=1' -H 'cookie: d=xoxd-q'`,
			wantURL:   "https://acme.slack.com",
			wantToken: "xoxc-qs-tok",
			wantXOXD:  "xoxd-q",
		},
		"multipart name=token field": {
			input:     "curl 'https://acme.slack.com/api/x' -b 'd=xoxd-m' --data-raw $'name=\"token\"\\r\\n\\r\\nxoxc-mp-tok'",
			wantURL:   "https://acme.slack.com",
			wantToken: "xoxc-mp-tok",
			wantXOXD:  "xoxd-m",
		},
		"uppercase host normalizes to lowercase": {
			input:     `curl 'https://ACME.slack.com/api/x' -b 'd=xoxd-u' --data 'token=xoxc-up-tok'`,
			wantURL:   "https://acme.slack.com",
			wantToken: "xoxc-up-tok",
			wantXOXD:  "xoxd-u",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			team, xoxd, err := ParseCurl(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if team.URL != tc.wantURL || team.Token != tc.wantToken || xoxd != tc.wantXOXD {
				t.Errorf("got %+v xoxd=%q", team, xoxd)
			}
		})
	}
}

func TestParseCurlErrors(t *testing.T) {
	cases := map[string]string{
		"no url":    `curl 'https://example.com/api' -b 'd=xoxd-x' --data 'token=xoxc-y'`,
		"no cookie": `curl 'https://acme.slack.com/api' --data 'token=xoxc-y'`,
		"no token":  `curl 'https://acme.slack.com/api' -b 'd=xoxd-x'`,
	}
	for name, input := range cases {
		if _, _, err := ParseCurl(input); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
