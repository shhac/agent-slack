package slack

import "os"

// Custom (workspace) emoji are addressed by name (:partyparrot:) and returned
// by emoji.list as a name→value map, where value is either an image URL or
// "alias:<other-name>". A single per-workspace cache ("emoji") backs `emoji
// list`/`get`: there is no separate id, so name is the key and one store
// suffices (unlike usergroups, which need a handle→id index alongside the
// entity store). This cache complements the static emojilib unicode table in
// internal/render — it holds only the workspace's custom set and aliases.
//
// emoji.list returns the whole set in one (optionally paged) sweep, so a fetch
// always enumerates everything and arms the completeness sentinel; a later
// name miss within the window is then authoritative ("no such custom emoji")
// and need not re-fetch.

func validEmoji(_ string, e CustomEmoji) bool { return e.Name != "" }

func (c *Client) emojiCache() *cacheSnapshot[CustomEmoji] {
	return openCacheFor(c, "emoji", cacheTTLOf(c.cache).Emoji, validEmoji)
}

// emojiComplete reports whether the custom emoji set was fully enumerated
// within the completeness window — so a name miss is authoritative.
func (c *Client) emojiComplete() bool {
	return c.emojiCache().isComplete(cacheTTLOf(c.cache).EmojiComplete)
}

// warmEmojiCache records a fetched custom emoji set into the cache and, when
// the set is complete (a full emoji.list sweep), arms the completeness
// sentinel. Batched (one save) and best-effort.
func (c *Client) warmEmojiCache(emoji []CustomEmoji, complete bool) {
	snap := c.emojiCache()
	for _, e := range emoji {
		if e.Name == "" {
			continue
		}
		snap.set(e.Name, e)
	}
	if complete {
		snap.markComplete()
	}
	snap.save()
}

// cachedEmojiSet returns the complete custom emoji set from cache when it was
// fully enumerated within the completeness window, else (nil, false) so the
// caller fetches live.
func (c *Client) cachedEmojiSet() (map[string]CustomEmoji, bool) {
	snap := c.emojiCache()
	if !snap.isComplete(cacheTTLOf(c.cache).EmojiComplete) {
		return nil, false
	}
	out := make(map[string]CustomEmoji, len(snap.data.Entries))
	for name, e := range snap.data.Entries {
		out[name] = e.Value
	}
	return out, true
}

// forgetEmojiCache drops the workspace's emoji cache file so the next list/get
// re-fetches. Called after a successful add/remove, which would otherwise leave
// the cached set (and its completeness sentinel) stale. Best-effort.
func (c *Client) forgetEmojiCache() {
	if c == nil || c.cache == nil {
		return
	}
	if path := cacheFilePath(c.cache.Dir, c.currentAuth().WorkspaceURL, "emoji"); path != "" {
		_ = os.Remove(path)
	}
}
