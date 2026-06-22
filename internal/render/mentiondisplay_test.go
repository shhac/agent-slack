package render

import (
	"sort"
	"testing"
)

func testResolvers() MentionResolvers {
	return MentionResolvers{
		User: func(id string) string {
			if id == "U0123456789" {
				return "Alice"
			}
			return ""
		},
		Channel: func(id string) string {
			if id == "C0123456789" {
				return "general"
			}
			return ""
		},
		Usergroup: func(id string) string {
			if id == "S0123456789" {
				return "eng"
			}
			return ""
		},
	}
}

func TestResolveMentionsForDisplay(t *testing.T) {
	r := testResolvers()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"user resolved", "ping @U0123456789 now", "ping @Alice now"},
		{"user unresolved stays raw", "ping @U9999999999 now", "ping @U9999999999 now"},
		{"user butted against prose", "useful @U0123456789for you", "useful @Alicefor you"},
		{"bare channel resolved", "see <#C0123456789>", "see #general"},
		{"bare channel unresolved stays", "see <#C9999999999>", "see <#C9999999999>"},
		{"labeled channel prefers resolver", "see <#C0123456789|old>", "see #general"},
		{"usergroup resolved", "cc <!subteam^S0123456789>", "cc @eng"},
		{"usergroup unresolved uses label", "cc <!subteam^S9999999999|@team>", "cc @team"},
		{"slack user link resolved", "hi <slack://user?team=T1&id=U0123456789|@paul>", "hi @Alice"},
		{"slack user link label fallback", "hi <slack://user?team=T1&id=U9999999999|@paul>", "hi @paul"},
		{"slack channel link uses label", "open <slack://channel?team=T1&id=C0B66NHRM6C|INC-2082: down>", "open INC-2082: down"},
		{"date token uses fallback", "due <!date^1782032540^{date}|June 21, 2026>", "due June 21, 2026"},
		{"mention inside code stays literal", "`@U0123456789`", "`@U0123456789`"},
		{"no tokens unchanged", "plain text", "plain text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveMentionsForDisplay(tc.in, r); got != tc.want {
				t.Errorf("ResolveMentionsForDisplay(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveMentionsForDisplayNilResolvers(t *testing.T) {
	// nil resolvers: id-bearing tokens stay raw, but slack:// links and date
	// tokens still clean up from their own label/fallback.
	var zero MentionResolvers
	if got := ResolveMentionsForDisplay("a @U0123456789 b", zero); got != "a @U0123456789 b" {
		t.Errorf("unresolved user should stay raw, got %q", got)
	}
	if got := ResolveMentionsForDisplay("x <slack://channel?id=C0123456789|Title> y", zero); got != "x Title y" {
		t.Errorf("slack channel link should use label even with nil resolvers, got %q", got)
	}
}

func TestApplyHyperlinks(t *testing.T) {
	enc := func(url, label string) string { return "<" + label + "|" + url + ">" }
	in := "see [here](https://x.com/a) and [docs](https://y.com)"
	want := "see <here|https://x.com/a> and <docs|https://y.com>"
	if got := ApplyHyperlinks(in, enc); got != want {
		t.Errorf("ApplyHyperlinks = %q, want %q", got, want)
	}
	// nil encoder is a no-op (the plain/LLM path keeps markdown).
	if got := ApplyHyperlinks(in, nil); got != in {
		t.Errorf("nil encoder should be a no-op, got %q", got)
	}
}

func TestCollectDisplayIDs(t *testing.T) {
	refs := CollectDisplayIDs(
		"hi @U0123456789 and @U0123456789",
		"in <#C0123456789> cc <!subteam^S0123456789>",
		"noise @notanid <#bad>",
	)
	sort.Strings(refs.Users)
	if len(refs.Users) != 1 || refs.Users[0] != "U0123456789" {
		t.Errorf("Users = %v, want [U0123456789] (deduped)", refs.Users)
	}
	if len(refs.Channels) != 1 || refs.Channels[0] != "C0123456789" {
		t.Errorf("Channels = %v, want [C0123456789]", refs.Channels)
	}
	if len(refs.Usergroups) != 1 || refs.Usergroups[0] != "S0123456789" {
		t.Errorf("Usergroups = %v, want [S0123456789]", refs.Usergroups)
	}
}
