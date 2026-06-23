package cli

import (
	"bytes"
	"context"
	stderrors "errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInlineEmojiResolverGating — the --images gate must yield no resolver
// (leaving emoji as text, no escape bytes) for off/auto-on-non-TTY/bad modes.
// These branches return before touching the client, so a nil clientContext is
// safe; "on" is excluded as it would proceed to fetch.
func TestInlineEmojiResolverGating(t *testing.T) {
	for _, mode := range []string{"off", "", "auto", "bogus"} {
		g := &GlobalFlags{}
		g.Images = mode
		g.stdout = &bytes.Buffer{} // never a TTY
		if r := inlineEmojiResolver(context.Background(), g, nil); r != nil {
			t.Errorf("inlineEmojiResolver(%q) on a non-TTY must be nil", mode)
		}
	}
}

// TestEmojiImageCacheDiskHit — a PNG already on disk (a prior run) is returned
// without a network fetch, exercising the cross-run cache keyed by URL.
func TestEmojiImageCacheDiskHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	const url = "u-parrot"
	if err := os.MkdirAll(emojiImagesDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	png := pngBytesFixture(t, color.RGBA{0, 0, 255, 255})
	if err := os.WriteFile(filepath.Join(emojiImagesDir(), emojiCacheFile(url)), png, 0o600); err != nil {
		t.Fatal(err)
	}

	fetched := 0
	fetch := func(context.Context, string) ([]byte, error) {
		fetched++
		return nil, stderrors.New("must not fetch on a disk hit")
	}
	cache := newEmojiImageCache(context.Background(), fetch, map[string]string{"parrot": url})

	if esc := cache.escape("parrot"); esc == "" {
		t.Error("a disk-cached emoji should still produce an escape")
	}
	if fetched != 0 {
		t.Errorf("disk hit must not fetch, got %d fetches", fetched)
	}
}

func pngBytesFixture(t *testing.T, c color.Color) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 4, 4))
	im.Set(0, 0, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gifBytesFixture(t *testing.T) []byte {
	t.Helper()
	im := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, im, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestToPNG(t *testing.T) {
	orig := pngBytesFixture(t, color.RGBA{1, 2, 3, 255})
	if got, err := toPNG(orig); err != nil || !bytes.Equal(got, orig) {
		t.Errorf("PNG should pass through untouched (err=%v, equal=%v)", err, bytes.Equal(got, orig))
	}

	out, err := toPNG(gifBytesFixture(t))
	if err != nil {
		t.Fatalf("GIF should decode to PNG: %v", err)
	}
	if !bytes.HasPrefix(out, pngMagic) {
		t.Errorf("GIF output is not PNG-encoded")
	}

	if _, err := toPNG([]byte("not an image")); err == nil {
		t.Errorf("undecodable bytes should error (so caller falls back to text)")
	}
}

func TestEmojiImageCacheEscape(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // isolate the disk cache

	calls := 0
	fetch := func(_ context.Context, url string) ([]byte, error) {
		calls++
		if url == "u-parrot" {
			return pngBytesFixture(t, color.RGBA{255, 0, 0, 255}), nil
		}
		return nil, stderrors.New("404")
	}
	cache := newEmojiImageCache(context.Background(), fetch, map[string]string{
		"parrot": "u-parrot",
		"broken": "u-broken",
	})

	first := cache.escape("parrot")
	if !strings.Contains(first, "\x1b_Ga=T") {
		t.Fatalf("first escape should transmit: %q", first[:min(40, len(first))])
	}

	second := cache.escape("parrot")
	if !strings.Contains(second, "a=p") {
		t.Errorf("repeat should re-place by reference: %q", second)
	}
	if calls != 1 {
		t.Errorf("image bytes should be fetched once and reused, got %d fetches", calls)
	}

	if got := cache.escape("unknown"); got != "" {
		t.Errorf("name not in url map should yield no escape, got %q", got)
	}
	if got := cache.escape("broken"); got != "" {
		t.Errorf("failed fetch should yield no escape (text fallback), got %q", got)
	}
}

func TestEmojiCacheFile(t *testing.T) {
	a := emojiCacheFile("https://example.com/a.png")
	b := emojiCacheFile("https://example.com/b.png")
	if a == b {
		t.Errorf("distinct URLs should map to distinct cache files")
	}
	if !strings.HasSuffix(a, ".png") || a != emojiCacheFile("https://example.com/a.png") {
		t.Errorf("cache file should be deterministic and .png suffixed: %q", a)
	}
}
