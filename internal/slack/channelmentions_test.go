package slack

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// channelMentionClient builds a client whose workspace has "#general" → C0GENERAL,
// resolvable via the single-call search trick (no conversations.list pagination).
func channelMentionClient(t *testing.T, server *mockslack.Server) *Client {
	t.Helper()
	server.HandleWhen("search.messages", func(p url.Values) bool {
		return p.Get("query") == "in:#general"
	}, mockslack.Response{Body: mockslack.SearchMessages(mockslack.ChannelMatch("C0GENERAL"))})
	return cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())
}

func TestResolveChannelMentions(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare name", "see #general please", "see <#C0GENERAL> please"},
		{"start of string", "#general is the place", "<#C0GENERAL> is the place"},
		{"heading stays literal", "# General notes", "# General notes"},
		{"sharp suffix stays literal", "I write C# and F#", "I write C# and F#"},
		{"all-digit ref untouched", "fixes #5 and #1234", "fixes #5 and #1234"},
		{"unknown channel stays literal", "ping #nope-not-real", "ping #nope-not-real"},
		{"preformed token untouched", "go to <#C0GENERAL>", "go to <#C0GENERAL>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := channelMentionClient(t, mockslack.New())
			if got := ResolveChannelMentions(context.Background(), c, tc.in); got != tc.want {
				t.Errorf("ResolveChannelMentions(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A #name inside a code span or fenced block stays literal.
func TestResolveChannelMentionsMasksCode(t *testing.T) {
	c := channelMentionClient(t, mockslack.New())
	in := "real #general but `#general` and\n```\n#general\n```"
	got := ResolveChannelMentions(context.Background(), c, in)
	if !strings.Contains(got, "real <#C0GENERAL> but `#general`") {
		t.Errorf("code-masked #general should stay literal: %q", got)
	}
	if strings.Count(got, "<#C0GENERAL>") != 1 {
		t.Errorf("only the prose #general should resolve: %q", got)
	}
}

// Resolution is cache-first then ONE search call — never the conversations.list
// pagination fallback, even when several unknown #words appear.
func TestResolveChannelMentionsNeverPaginates(t *testing.T) {
	server := mockslack.New()
	c := channelMentionClient(t, server)
	ResolveChannelMentions(context.Background(), c, "#general #foo #bar #foo")
	if n := len(server.CallsFor("conversations.list")); n != 0 {
		t.Errorf("conversations.list called %d times, want 0 (cheap resolution only)", n)
	}
	// #general resolves (1 search); #foo and #bar each search once (deduped).
	if n := len(server.CallsFor("search.messages")); n != 3 {
		t.Errorf("search.messages called %d times, want 3 (general, foo, bar)", n)
	}
}
