package auth

import "testing"

// TestXoxdFromPlain covers Slack's cookie-value policy applied on top of the
// shared library's verbatim output: find the xoxd- token and URL-decode it once.
func TestXoxdFromPlain(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain token", "xoxd-RealValue123", "xoxd-RealValue123", false},
		{"url-decoded once", "xoxd-Ab%2FCd", "xoxd-Ab/Cd", false},
		{"scans past leading bytes", "leading-junk xoxd-Ab%2FCd", "xoxd-Ab/Cd", false},
		{"no token errors", "no-token-here", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := xoxdFromPlain([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected an error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("xoxdFromPlain(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
