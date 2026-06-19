package slack

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func searchFixture() map[string]any {
	return map[string]any{"ok": true, "emoji": map[string]any{
		"parrot":       "https://e/parrot.gif",
		"party-parrot": "https://e/party-parrot.gif",
		"partyparrot":  "https://e/partyparrot.gif",
		"sad_parrot":   "https://e/sad_parrot.gif",
		"rocket_ship":  "https://e/rocket_ship.gif",
		"shipit":       "alias:party-parrot",
		"prrot":        "https://e/prrot.gif", // one deletion from "parrot" → fuzzy
	}}
}

func searchClient(t *testing.T) *Client {
	server := mockslack.New()
	server.HandleBody("emoji.list", searchFixture())
	return cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())
}

func TestSearchEmojiTiersAndOrdering(t *testing.T) {
	c := searchClient(t)
	matches, next, err := SearchEmoji(context.Background(), c, "parrot", SearchEmojiOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Errorf("small result set should fit one page, got cursor %q", next)
	}

	byName := map[string]EmojiMatch{}
	for _, m := range matches {
		byName[m.Name] = m
	}
	// Exact (after folding separators): parrot, party-parrot→"partyparrot"? no.
	// "parrot" folds to "parrot"; only "parrot" is exact.
	if byName["parrot"].Match != "exact" || byName["parrot"].Score != 1.0 {
		t.Errorf("parrot = %+v, want exact 1.0", byName["parrot"])
	}
	// "partyparrot" and "party-parrot" both fold to "partyparrot" → token_prefix
	// ("parrot" is a token / suffix, not a prefix of the whole key).
	if byName["party-parrot"].Match != "token_prefix" {
		t.Errorf("party-parrot = %+v, want token_prefix", byName["party-parrot"])
	}
	// "sad_parrot": "parrot" is a token prefix.
	if byName["sad_parrot"].Match != "token_prefix" {
		t.Errorf("sad_parrot = %+v, want token_prefix", byName["sad_parrot"])
	}
	// "prrot" is one edit from "parrot" → fuzzy.
	if byName["prrot"].Match != "fuzzy" {
		t.Errorf("prrot = %+v, want fuzzy", byName["prrot"])
	}
	// rocket_ship doesn't match "parrot" at all.
	if _, ok := byName["rocket_ship"]; ok {
		t.Errorf("rocket_ship should not match 'parrot'")
	}
	// Exact sorts first.
	if matches[0].Name != "parrot" {
		t.Errorf("first result = %q, want parrot (exact ranks first)", matches[0].Name)
	}
	// Scores are non-increasing.
	for i := 1; i < len(matches); i++ {
		if matches[i].Score > matches[i-1].Score {
			t.Errorf("results not sorted by score: %v", matches)
		}
	}
}

// Folding: ":Party_Parrot:" matches "partyparrot" / "party-parrot" despite
// case, colons, and separators.
func TestSearchEmojiNormalization(t *testing.T) {
	c := searchClient(t)
	matches, _, err := SearchEmoji(context.Background(), c, ":Party_Parrot:", SearchEmojiOptions{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range matches {
		names[m.Name] = true
	}
	if !names["partyparrot"] || !names["party-parrot"] {
		t.Errorf("normalized query should match both partyparrot forms, got %v", names)
	}
}

func TestSearchEmojiPagination(t *testing.T) {
	c := searchClient(t)
	// "parrot" matches several; page size 2 forces a cursor.
	page1, next, err := SearchEmoji(context.Background(), c, "parrot", SearchEmojiOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || next == "" {
		t.Fatalf("page1 = %d items, next = %q; want 2 + a cursor", len(page1), next)
	}
	page2, _, err := SearchEmoji(context.Background(), c, "parrot", SearchEmojiOptions{Limit: 2, Cursor: next})
	if err != nil {
		t.Fatal(err)
	}
	// Pages don't overlap.
	for _, a := range page1 {
		for _, b := range page2 {
			if a.Name == b.Name {
				t.Errorf("page overlap on %q", a.Name)
			}
		}
	}
}

func TestSearchEmojiLimitCapAndFull(t *testing.T) {
	c := searchClient(t)
	// Full includes URLs; default omits them.
	full, _, err := SearchEmoji(context.Background(), c, "parrot", SearchEmojiOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range full {
		if m.AliasFor == "" && m.URL == "" {
			t.Errorf("--full should carry URL for non-alias %q", m.Name)
		}
	}
	lean, _, _ := SearchEmoji(context.Background(), c, "parrot", SearchEmojiOptions{})
	for _, m := range lean {
		if m.URL != "" {
			t.Errorf("lean result should omit URL, got %q for %q", m.URL, m.Name)
		}
	}
}

func TestSearchEmojiBadCursor(t *testing.T) {
	c := searchClient(t)
	if _, _, err := SearchEmoji(context.Background(), c, "parrot", SearchEmojiOptions{Cursor: "!!!not-base64!!!"}); err == nil {
		t.Error("want an error for a malformed cursor")
	}
}

func TestBoundedLevenshtein(t *testing.T) {
	cases := []struct {
		a, b   string
		max    int
		want   int
		within bool
	}{
		{"parrot", "prrot", 2, 1, true},
		{"parrot", "parrot", 2, 0, true},
		{"parrot", "rocket", 2, 0, false},
		{"abc", "abcdef", 2, 0, false}, // length gap exceeds bound
	}
	for _, tc := range cases {
		d, ok := boundedLevenshtein(tc.a, tc.b, tc.max)
		if ok != tc.within || (ok && d != tc.want) {
			t.Errorf("levenshtein(%q,%q,%d) = (%d,%v), want (%d,%v)", tc.a, tc.b, tc.max, d, ok, tc.want, tc.within)
		}
	}
}

// The limit bounds: an over-cap limit clamps to the max page size, and a
// negative limit clamps to the floor (1) — not the default — since a limit is
// an upper bound and must never yield more rows than requested.
func TestSearchEmojiLimitBounds(t *testing.T) {
	emoji := map[string]any{}
	for i := 0; i < 150; i++ { // > maxEmojiSearchLimit (100), all match "parrot"
		emoji[fmt.Sprintf("parrot%03d", i)] = "https://e/p.gif"
	}
	server := mockslack.New()
	server.HandleBody("emoji.list", map[string]any{"ok": true, "emoji": emoji})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())
	ctx := context.Background()

	page, next, err := SearchEmoji(ctx, c, "parrot", SearchEmojiOptions{Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != maxEmojiSearchLimit || next == "" {
		t.Errorf("over-cap limit: got %d items next=%q, want %d + a cursor", len(page), next, maxEmojiSearchLimit)
	}

	page, _, err = SearchEmoji(ctx, c, "parrot", SearchEmojiOptions{Limit: -5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 {
		t.Errorf("negative limit: got %d items, want 1 (clamped to floor, not default)", len(page))
	}
}
