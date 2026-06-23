package slack

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

type emojiDoerFunc func(*http.Request) (*http.Response, error)

func (f emojiDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

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

// TestCustomEmojiImageURLs drives the whole name→URL map build through a
// mockslack emoji.list, covering alias resolution and the omit-non-image rule.
func TestCustomEmojiImageURLs(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", map[string]any{"ok": true, "emoji": map[string]any{
		"parrot":   "https://cdn/parrot.gif",
		"ship":     "alias:parrot", // alias onto a custom image → resolves
		"tostd":    "alias:smile",  // alias onto a standard (non-custom) name → omitted
		"dangling": "alias:ghost",  // alias onto a missing name → omitted
	}})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	urls, err := CustomEmojiImageURLs(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if urls["parrot"] != "https://cdn/parrot.gif" {
		t.Errorf("parrot = %q", urls["parrot"])
	}
	if urls["ship"] != "https://cdn/parrot.gif" {
		t.Errorf("alias should resolve to the target image, got %q", urls["ship"])
	}
	if _, ok := urls["tostd"]; ok {
		t.Error("alias onto a standard emoji should be omitted (no custom image)")
	}
	if _, ok := urls["dangling"]; ok {
		t.Error("dangling alias should be omitted")
	}
	if len(urls) != 2 {
		t.Errorf("want 2 image entries (parrot, ship), got %d: %v", len(urls), urls)
	}
}

// TestFetchBytes covers the credential-bearing emoji-image fetch: auth header
// set, body returned on 2xx, error on non-2xx, and no token leak into the error.
func TestFetchBytes(t *testing.T) {
	var gotAuth string
	doer := emojiDoerFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/missing") {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("nope"))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("PNGBYTES"))}, nil
	})
	c := New(Auth{Type: AuthStandard, Token: "xoxb-secret"}, WithDoer(doer))

	b, err := c.FetchBytes(context.Background(), "https://emoji.cdn/ok.png")
	if err != nil {
		t.Fatalf("FetchBytes: %v", err)
	}
	if string(b) != "PNGBYTES" {
		t.Errorf("body = %q, want PNGBYTES", b)
	}
	if gotAuth != "Bearer xoxb-secret" {
		t.Errorf("auth header = %q, want Bearer xoxb-secret", gotAuth)
	}

	_, err = c.FetchBytes(context.Background(), "https://emoji.cdn/missing")
	if err == nil {
		t.Fatal("non-2xx status should error")
	}
	if strings.Contains(err.Error(), "xoxb-secret") {
		t.Error("token must not leak into the error message")
	}
}
