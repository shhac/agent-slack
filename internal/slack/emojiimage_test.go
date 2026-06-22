package slack

import "testing"

func TestResolveCustomEmojiURL(t *testing.T) {
	byName := map[string]CustomEmoji{
		"parrot":      {Name: "parrot", URL: "https://cdn/parrot.gif"},
		"party":       {Name: "party", AliasFor: "parrot"},     // alias onto a custom image
		"to_standard": {Name: "to_standard", AliasFor: "smile"}, // alias onto a non-custom name
		"dangling":    {Name: "dangling", AliasFor: "ghost"},    // alias onto a missing name
	}

	cases := []struct {
		name string
		want string
	}{
		{"parrot", "https://cdn/parrot.gif"},
		{"party", "https://cdn/parrot.gif"}, // followed one hop
		{"to_standard", ""},                 // resolves to no custom image
		{"dangling", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveCustomEmojiURL(byName[tc.name], byName); got != tc.want {
				t.Errorf("resolveCustomEmojiURL(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
