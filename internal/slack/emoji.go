package slack

import (
	"context"
	"slices"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// CustomEmoji is the token-lean projection of one workspace custom emoji.
// Exactly one of URL (a custom image) or AliasFor (this name points at another
// emoji) is set.
type CustomEmoji struct {
	Name     string `json:"name"`
	URL      string `json:"url,omitempty"`       // image URL; empty when this is an alias
	AliasFor string `json:"alias_for,omitempty"` // target name when value was "alias:<name>"
}

// EmojiResult is the resolved shape returned by `emoji get`: a unified lookup
// over the workspace custom set and the static standard (unicode) set. For an
// alias, AliasFor records the original target and the resolved image/char is
// filled from following it one hop.
type EmojiResult struct {
	Name     string `json:"name"`
	Custom   bool   `json:"custom"`              // true when defined as a workspace custom emoji
	URL      string `json:"url,omitempty"`       // custom image URL (after alias resolution)
	Unicode  string `json:"unicode,omitempty"`   // standard emoji character (after alias resolution)
	AliasFor string `json:"alias_for,omitempty"` // original alias target, before resolution
}

// toCustomEmoji shapes one emoji.list entry: "alias:foo" becomes an alias,
// anything else is treated as an image URL.
func toCustomEmoji(name, value string) CustomEmoji {
	if target, ok := strings.CutPrefix(value, "alias:"); ok {
		return CustomEmoji{Name: name, AliasFor: target}
	}
	return CustomEmoji{Name: name, URL: value}
}

// ListEmojiOptions controls ListEmoji.
type ListEmojiOptions struct {
	Full   bool   // include image URLs (omitted by default to keep list output lean)
	Limit  int    // page size; <=0 uses defaultEmojiListLimit, capped at maxEmojiListLimit
	Cursor string // opaque offset cursor from a previous page
}

const (
	defaultEmojiListLimit = 200
	maxEmojiListLimit     = 1000
)

// ListEmoji returns one page of the workspace's custom emoji, sorted by name,
// plus the cursor for the next page (empty when exhausted). It serves the
// complete cached set when fresh, else fetches emoji.list (which also warms the
// cache). Without opts.Full, image URLs are omitted — the lean default surfaces
// which names exist and how aliases resolve. Paginated (a busy workspace can
// have thousands of custom emoji) with the same opaque offset cursor as search.
func ListEmoji(ctx context.Context, c *Client, opts ListEmojiOptions) ([]CustomEmoji, string, error) {
	offset, err := decodeOffsetCursor(opts.Cursor)
	if err != nil {
		return nil, "", err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultEmojiListLimit
	}
	if limit > maxEmojiListLimit {
		limit = maxEmojiListLimit
	}

	byName, err := c.customEmojiMap(ctx)
	if err != nil {
		return nil, "", err
	}
	all := make([]CustomEmoji, 0, len(byName))
	for _, e := range byName {
		if !opts.Full {
			e.URL = ""
		}
		all = append(all, e)
	}
	slices.SortFunc(all, func(a, b CustomEmoji) int { return strings.Compare(a.Name, b.Name) })

	if offset >= len(all) {
		return nil, "", nil
	}
	end := min(offset+limit, len(all))
	next := ""
	if end < len(all) {
		next = encodeOffsetCursor(end)
	}
	return all[offset:end], next, nil
}

// GetEmoji resolves one emoji name (with or without surrounding colons) over
// both the workspace custom set and the static standard set. Aliases are
// followed one hop. Returns a human-fixable not-found when nothing matches.
func GetEmoji(ctx context.Context, c *Client, input string) (EmojiResult, error) {
	name := normalizeEmojiLookup(input)
	if name == "" {
		return EmojiResult{}, agenterrors.New("emoji name is empty", agenterrors.FixableByAgent)
	}
	byName, err := c.customEmojiMap(ctx)
	if err != nil {
		return EmojiResult{}, err
	}
	if res, ok := resolveEmoji(name, byName); ok {
		return res, nil
	}
	return EmojiResult{}, errEmojiNotFound(input)
}

// resolveEmoji is the pure resolution behind `emoji get`: custom emoji win
// over standard ones; an alias is followed one hop into the custom set, then
// the standard set. Reports false when the name is in neither.
func resolveEmoji(name string, custom map[string]CustomEmoji) (EmojiResult, bool) {
	if e, ok := custom[name]; ok {
		if e.AliasFor == "" {
			return EmojiResult{Name: name, Custom: true, URL: e.URL}, true
		}
		res := EmojiResult{Name: name, Custom: true, AliasFor: e.AliasFor}
		if target, ok := custom[e.AliasFor]; ok && target.URL != "" {
			res.URL = target.URL
		} else if u, ok := render.EmojiUnicode(e.AliasFor); ok {
			res.Unicode = u
		}
		return res, true
	}
	if u, ok := render.EmojiUnicode(name); ok {
		return EmojiResult{Name: name, Unicode: u}, true
	}
	return EmojiResult{}, false
}

// customEmojiMap returns the workspace custom emoji keyed by name, served from
// the complete cached set when fresh, else fetched live. Shared by list and
// get so both pay one fetch at most.
func (c *Client) customEmojiMap(ctx context.Context) (map[string]CustomEmoji, error) {
	if set, ok := c.cachedEmojiSet(); ok {
		return set, nil
	}
	emoji, err := fetchEmoji(ctx, c)
	if err != nil {
		return nil, err
	}
	out := make(map[string]CustomEmoji, len(emoji))
	for _, e := range emoji {
		out[e.Name] = e
	}
	return out, nil
}

// fetchEmoji calls emoji.list to completion (threading the cursor when the API
// pages) and warms the cache with the full set. emoji.list returns the emoji
// as a name→value object per page.
func fetchEmoji(ctx context.Context, c *Client) ([]CustomEmoji, error) {
	var emoji []CustomEmoji
	err := EachPage(ctx, c, "emoji.list", map[string]any{"limit": 1000}, func(resp map[string]any) (bool, error) {
		for name, v := range getRec(resp, "emoji") {
			value, ok := v.(string)
			if name == "" || !ok {
				continue
			}
			emoji = append(emoji, toCustomEmoji(name, value))
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	c.warmEmojiCache(emoji, true)
	return emoji, nil
}

// normalizeEmojiLookup prepares a get input for exact name lookup: trim space,
// strip surrounding colons, lowercase. Separators (-_+) are NOT collapsed —
// custom emoji names are exact, so thumbs-up and thumbsup are distinct.
func normalizeEmojiLookup(input string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(input), ":"))
}

func errEmojiNotFound(input string) *agenterrors.APIError {
	return errResolveFailed("emoji: "+normalizeEmojiLookup(input),
		"pass a known emoji name — 'agent-slack emoji list' shows custom emoji")
}
