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

	emoji, err := ListEmoji(context.Background(), c, ListEmojiOptions{})
	if err != nil {
		t.Fatal(err)
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

	emoji, err := ListEmoji(context.Background(), c, ListEmojiOptions{Full: true})
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

	if _, err := ListEmoji(context.Background(), c, ListEmojiOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := GetEmoji(context.Background(), c, "partyparrot"); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("emoji.list")); n != 1 {
		t.Errorf("want exactly 1 emoji.list call, got %d", n)
	}
}
