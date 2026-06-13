package auth

import "testing"

func TestSelectSafariSlackCookie(t *testing.T) {
	cases := []struct {
		name    string
		cookies []binaryCookie
		want    string
		ok      bool
	}{
		{
			name:    "matching d cookie wins and is URL-decoded",
			cookies: []binaryCookie{{Domain: ".slack.com", Name: "d", Value: "xoxd-a%2Fb"}},
			want:    "xoxd-a/b",
			ok:      true,
		},
		{
			name:    "picks the slack d cookie among others",
			cookies: []binaryCookie{{Domain: ".example.com", Name: "sid", Value: "nope"}, {Domain: "app.slack.com", Name: "d", Value: "xoxd-real"}},
			want:    "xoxd-real",
			ok:      true,
		},
		{
			name:    "wrong name is rejected",
			cookies: []binaryCookie{{Domain: ".slack.com", Name: "d-s", Value: "xoxd-x"}},
			ok:      false,
		},
		{
			name:    "non-slack domain is rejected",
			cookies: []binaryCookie{{Domain: ".notslack.example", Name: "d", Value: "xoxd-x"}},
			ok:      false,
		},
		{
			name:    "non-xoxd value is rejected",
			cookies: []binaryCookie{{Domain: ".slack.com", Name: "d", Value: "something-else"}},
			ok:      false,
		},
		{
			name: "empty set yields nothing",
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := selectSafariSlackCookie(tc.cookies)
			if ok != tc.ok || got != tc.want {
				t.Errorf("selectSafariSlackCookie = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}
