package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func emojiListBody() map[string]any {
	return map[string]any{"ok": true, "emoji": map[string]any{
		"partyparrot": "https://emoji.slack-edge.com/T1/partyparrot/abc.gif",
		"shipit":      "alias:squirrel",
		"squirrel":    "https://emoji.slack-edge.com/T1/squirrel/def.png",
		"yay":         "alias:+1",       // alias to a standard emoji emojilib knows
		"hmm":         "alias:thumbsup", // alias to a standard name emojilib lacks
	}}
}

func TestListEmojiSortedAndLean(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	emoji, next, err := ListEmoji(context.Background(), c, ListEmojiOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Errorf("small set should fit one page, got cursor %q", next)
	}
	if len(emoji) != 5 {
		t.Fatalf("want 5 emoji, got %d: %+v", len(emoji), emoji)
	}
	// Sorted by name.
	want := []string{"hmm", "partyparrot", "shipit", "squirrel", "yay"}
	for i, e := range emoji {
		if e.Name != want[i] {
			t.Errorf("emoji[%d] = %q, want %q", i, e.Name, want[i])
		}
	}
	// Lean default: URLs omitted, alias targets kept.
	for _, e := range emoji {
		if e.URL != "" {
			t.Errorf("%s: URL should be omitted without --full, got %q", e.Name, e.URL)
		}
	}
	if emoji[2].AliasFor != "squirrel" {
		t.Errorf("shipit alias_for = %q, want squirrel", emoji[2].AliasFor)
	}
}

func TestListEmojiFullKeepsURLs(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	emoji, _, err := ListEmoji(context.Background(), c, ListEmojiOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	var pp CustomEmoji
	for _, e := range emoji {
		if e.Name == "partyparrot" {
			pp = e
		}
	}
	if pp.URL == "" {
		t.Errorf("--full should include URL, got empty for partyparrot")
	}
}

func TestListEmojiPagination(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody()) // 5 emoji
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	page1, next, err := ListEmoji(context.Background(), c, ListEmojiOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || next == "" {
		t.Fatalf("page1 = %d items, next = %q; want 2 + cursor", len(page1), next)
	}
	// Sorted across pages: page1 holds the first two names.
	if page1[0].Name != "hmm" || page1[1].Name != "partyparrot" {
		t.Errorf("page1 = %v, want [hmm partyparrot]", []string{page1[0].Name, page1[1].Name})
	}
	page2, _, err := ListEmoji(context.Background(), c, ListEmojiOptions{Limit: 2, Cursor: next})
	if err != nil {
		t.Fatal(err)
	}
	if page2[0].Name != "shipit" {
		t.Errorf("page2 first = %q, want shipit (continues the sort)", page2[0].Name)
	}
	// Walking past the end yields nothing, no error.
	beyond := encodeOffsetCursor(99)
	tail, next3, err := ListEmoji(context.Background(), c, ListEmojiOptions{Cursor: beyond})
	if err != nil || len(tail) != 0 || next3 != "" {
		t.Errorf("beyond-end page = (%d items, %q, %v), want empty", len(tail), next3, err)
	}
}

func TestGetEmojiCustomImage(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	// Colons are tolerated and the name is matched exactly.
	res, err := GetEmoji(context.Background(), c, ":partyparrot:")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Custom || res.URL == "" || res.Unicode != "" {
		t.Errorf("partyparrot = %+v, want custom image", res)
	}
}

func TestGetEmojiAliasToCustom(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	res, err := GetEmoji(context.Background(), c, "shipit")
	if err != nil {
		t.Fatal(err)
	}
	if res.AliasFor != "squirrel" || res.URL == "" {
		t.Errorf("shipit = %+v, want alias_for squirrel resolved to an image URL", res)
	}
}

func TestGetEmojiAliasToStandard(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	res, err := GetEmoji(context.Background(), c, "yay")
	if err != nil {
		t.Fatal(err)
	}
	if res.AliasFor != "+1" || res.Unicode == "" {
		t.Errorf("yay = %+v, want alias_for +1 resolved to a unicode char", res)
	}
}

// Slack's standard emoji names don't all match emojilib's (e.g. "thumbsup" vs
// "+1"). When an alias points at a name emojilib lacks, get still reports the
// alias target — it just can't fill the unicode char.
func TestGetEmojiAliasToUnknownStandard(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	res, err := GetEmoji(context.Background(), c, "hmm")
	if err != nil {
		t.Fatal(err)
	}
	if res.AliasFor != "thumbsup" || res.Unicode != "" {
		t.Errorf("hmm = %+v, want alias_for thumbsup with no resolved unicode", res)
	}
}

// A name that isn't custom but is a standard emojilib shortcode resolves to its
// unicode character without being marked custom.
func TestGetEmojiStandardFallback(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	res, err := GetEmoji(context.Background(), c, "rocket")
	if err != nil {
		t.Fatal(err)
	}
	if res.Custom || res.Unicode != "🚀" {
		t.Errorf("rocket = %+v, want standard 🚀", res)
	}
}

func TestGetEmojiNotFound(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	if _, err := GetEmoji(context.Background(), c, "no_such_emoji_xyz"); err == nil {
		t.Error("want not-found error for unknown emoji")
	}
}

// After a full fetch arms the completeness sentinel, a second list/get is served
// from cache with no further emoji.list call.
func TestEmojiCacheServesWithoutRefetch(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", emojiListBody())
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	if _, _, err := ListEmoji(context.Background(), c, ListEmojiOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetEmoji(context.Background(), c, "partyparrot"); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("emoji.list")); n != 1 {
		t.Errorf("want exactly 1 emoji.list call, got %d", n)
	}
}

// The alias one-hop guarantee: get follows an alias exactly one hop. An alias
// pointing at another alias is NOT chased to the second hop, and a dangling
// target resolves to neither a URL nor a unicode char.
func TestGetEmojiAliasOneHop(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", map[string]any{"ok": true, "emoji": map[string]any{
		"squirrel": "https://e/squirrel.png",
		"shipit":   "alias:squirrel",           // alias → custom (one hop, resolves)
		"chain":    "alias:shipit",             // alias → alias (must NOT chase second hop)
		"dangling": "alias:does_not_exist_xyz", // alias → nothing
	}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())
	ctx := context.Background()

	chain, err := GetEmoji(ctx, c, "chain")
	if err != nil {
		t.Fatal(err)
	}
	if chain.AliasFor != "shipit" || chain.URL != "" || chain.Unicode != "" {
		t.Errorf("chain = %+v; want alias_for=shipit, no URL/Unicode (one hop only)", chain)
	}

	dangling, err := GetEmoji(ctx, c, "dangling")
	if err != nil {
		t.Fatal(err)
	}
	if dangling.AliasFor != "does_not_exist_xyz" || dangling.URL != "" || dangling.Unicode != "" {
		t.Errorf("dangling = %+v; want alias_for set, no URL/Unicode", dangling)
	}

	// Sanity: the single-hop alias still resolves to its target's image.
	if shipit, _ := GetEmoji(ctx, c, "shipit"); shipit.URL == "" {
		t.Errorf("shipit = %+v; want resolved image URL", shipit)
	}
}
