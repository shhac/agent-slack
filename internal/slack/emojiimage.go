package slack

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// CustomEmojiImageURLs returns the workspace's custom emoji as a name→image-URL
// map, with aliases resolved one hop to the image they point at. Names that
// resolve to no image (an alias onto a standard unicode emoji, or a dangling
// alias) are omitted — the map holds only names that have a fetchable image.
// Served from the cached emoji set when fresh, else one emoji.list fetch.
//
// This backs the transcript renderer's inline-image mode; it is intentionally a
// whole-set fetch (cheap and cached) rather than per-name, since a transcript
// references many emoji at once.
func CustomEmojiImageURLs(ctx context.Context, c *Client) (map[string]string, error) {
	byName, err := c.customEmojiMap(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(byName))
	for name, e := range byName {
		if u := resolveCustomEmojiURL(e, byName); u != "" {
			out[name] = u
		}
	}
	return out, nil
}

// resolveCustomEmojiURL returns the image URL for one custom emoji, following an
// alias a single hop into the custom set. Returns "" when the emoji is an alias
// onto a non-custom (standard) name or a name that isn't present — matching the
// single-hop alias resolution resolveEmoji uses for `emoji get`.
func resolveCustomEmojiURL(e CustomEmoji, byName map[string]CustomEmoji) string {
	if e.URL != "" {
		return e.URL
	}
	if e.AliasFor != "" {
		if target, ok := byName[e.AliasFor]; ok {
			return target.URL
		}
	}
	return ""
}

// FetchBytes downloads url with the account's credentials and returns the body
// in memory. It is for small assets normalized before use (custom-emoji images
// decoded to PNG) rather than written verbatim — DownloadFile remains the
// path for user-facing file downloads that land on disk.
func (c *Client) FetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setDownloadAuthHeaders(req)

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
