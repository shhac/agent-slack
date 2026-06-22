package cli

import (
	"bytes"
	"context"
	stderrors "errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"strings"
	"testing"
)

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
