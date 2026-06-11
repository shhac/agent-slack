package render

import "testing"

func TestFormatOutboundText(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"promotes bare user ID", "@U05BRPTKL6A heads up", "<@U05BRPTKL6A> heads up"},
		{"promotes W and B IDs", "cc @W123456A and @BABCDEFG", "cc <@W123456A> and <@BABCDEFG>"},
		{"keeps formed mention", "hi <@U123456A>!", "hi <@U123456A>!"},
		{"keeps formed mention with nick", "hi <@U123456A|nick>!", "hi <@U123456A|nick>!"},
		{"keeps usergroup mention with label", "ping <!subteam^S12345678|@team>", "ping <!subteam^S12345678|@team>"},
		{"keeps bare usergroup mention", "ping <!subteam^S12345678>", "ping <!subteam^S12345678>"},
		{"promotes @here", "@here ping", "<!here> ping"},
		{"promotes @channel and @everyone", "cc @channel and @everyone", "cc <!channel> and <!everyone>"},
		{"escapes literals", "a < b && c > d", "a &lt; b &amp;&amp; c &gt; d"},
		{"keeps formed link", "see <https://example.com|link>", "see <https://example.com|link>"},
		{"keeps mailto link", "mail <mailto:bob@example.com|Bob>", "mail <mailto:bob@example.com|Bob>"},
		{"keeps query ampersand in link", "see <https://a.test/?x=1&y=2>", "see <https://a.test/?x=1&y=2>"},
		{"no email promotion", "mail me at user@Udomain.com", "mail me at user@Udomain.com"},
		{"empty", "", ""},
		{
			"real-world CI dump",
			`@U05BRPTKL6A heads up: CI "Install dependencies" is failing: https://github.com/x/y/actions/runs/1 & it needs <fix>`,
			`<@U05BRPTKL6A> heads up: CI "Install dependencies" is failing: https://github.com/x/y/actions/runs/1 &amp; it needs &lt;fix&gt;`,
		},
	}
	for _, tc := range cases {
		if got := FormatOutboundText(tc.input); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}
