package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"os"
	"path/filepath"

	// Registered for image.Decode: PNG passes through, GIF/JPEG decode to their
	// first frame. Kitty renders PNG (or raw pixels) only, so non-PNG emoji are
	// normalized to PNG before transmission. WebP is unsupported (no stdlib
	// decoder) and falls back to the text shortcode.
	_ "image/gif"
	_ "image/jpeg"

	graphics "github.com/shhac/lib-agent-cli/graphics"

	"github.com/shhac/agent-slack/internal/slack"
)

// emojiImagesDir is where one identity's decoded custom-emoji PNGs are cached
// between runs, beside its downloads under the per-identity cache subtree (key
// is <team_id>/<user_id>).
func emojiImagesDir(key string) string {
	return filepath.Join(appCacheDir(), key, "emoji-images")
}

// inlineEmojiResolver returns the TranscriptOptions.InlineEmoji seam, or nil to
// leave shortcodes as text. It is the single gate for the whole feature: the
// --images mode (off/auto/on) decides per stream via graphics.Active — off
// never draws, auto draws on a graphics-capable TTY, on forces. Any failure to
// set up (bad mode, no custom emoji, fetch error) degrades silently to nil, so
// the transcript still renders as text.
func inlineEmojiResolver(ctx context.Context, globals *GlobalFlags, cc *clientContext) func(name string) string {
	mode, err := graphics.ParseMode(globals.Images)
	if err != nil || !graphics.Active(globals.stdout, mode) {
		return nil
	}
	urls, err := slack.CustomEmojiImageURLs(ctx, cc.Client)
	if err != nil || len(urls) == 0 {
		return nil
	}
	cache := newEmojiImageCache(ctx, cc.Client.FetchBytes, urls, emojiImagesDir(cc.CacheKey))
	return cache.escape
}

// emojiImageCache turns custom-emoji names into inline-image escape sequences,
// deduplicating both the network fetch (disk + memory) and the terminal
// transmit (graphics.Encoder re-places a repeat by reference). It is scoped to
// one transcript render — IDs and the in-memory map need not outlive it.
type emojiImageCache struct {
	ctx   context.Context
	fetch func(ctx context.Context, url string) ([]byte, error)
	urls  map[string]string // name → image URL (aliases pre-resolved)
	dir   string            // on-disk emoji-image cache dir for this identity

	enc *graphics.Encoder
	ids map[string]uint32 // name → stable graphics image id
	png map[string][]byte // name → normalized PNG bytes (in-memory)
}

func newEmojiImageCache(ctx context.Context, fetch func(ctx context.Context, url string) ([]byte, error), urls map[string]string, dir string) *emojiImageCache {
	return &emojiImageCache{
		ctx:   ctx,
		fetch: fetch,
		urls:  urls,
		dir:   dir,
		enc:   graphics.NewEncoder(),
		ids:   map[string]uint32{},
		png:   map[string][]byte{},
	}
}

// escape resolves one emoji name to its inline-image escape, or "" to leave the
// shortcode as text (name isn't a custom emoji, or its image can't be fetched
// or decoded). cellHeight is 1: emoji sit one text row tall, inline.
func (e *emojiImageCache) escape(name string) string {
	url, ok := e.urls[name]
	if !ok {
		return ""
	}
	data := e.pngBytes(name, url)
	if data == nil {
		return ""
	}
	id, ok := e.ids[name]
	if !ok {
		id = uint32(len(e.ids)) + 1
		e.ids[name] = id
	}
	return e.enc.Inline(graphics.Image{ID: id, Data: data}, 1)
}

// pngBytes returns the emoji image as PNG, from memory, then disk, then network
// (normalizing to PNG on the way). Returns nil on any failure so the caller
// falls back to text.
func (e *emojiImageCache) pngBytes(name, url string) []byte {
	if data, ok := e.png[name]; ok {
		return data
	}

	path := filepath.Join(e.dir, emojiCacheFile(url))
	if data, err := os.ReadFile(path); err == nil {
		e.png[name] = data
		return data
	}

	raw, err := e.fetch(e.ctx, url)
	if err != nil {
		return nil
	}
	data, err := toPNG(raw)
	if err != nil {
		return nil
	}
	e.png[name] = data
	if err := os.MkdirAll(e.dir, 0o700); err == nil {
		_ = os.WriteFile(path, data, 0o600)
	}
	return data
}

// emojiCacheFile is the on-disk name for a cached emoji PNG: the URL's SHA-256,
// since the URL is the immutable identity of the image (a renamed emoji gets a
// new URL) and may contain path characters.
func emojiCacheFile(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:]) + ".png"
}

// pngMagic is the 8-byte PNG signature.
var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// toPNG normalizes encoded image bytes to PNG: already-PNG bytes pass through
// untouched; GIF (first frame) and JPEG are decoded and re-encoded. Returns an
// error for formats with no stdlib decoder (e.g. WebP).
func toPNG(data []byte) ([]byte, error) {
	if bytes.HasPrefix(data, pngMagic) {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
