package slack

import "strings"

// Channel resolution is the most-repeated awkward lookup: name→ID has no Slack
// API and falls back to paginating the whole workspace. Two per-workspace
// caches back it — a name→ID index and an ID→CompactChannel entity store —
// each a single-key open/get or open/set/save (channels are looked up one or
// two at a time per command, so re-reading the small file is cheap).

func validChannel(_ string, ch CompactChannel) bool { return ch.ID != "" }

func (c *Client) channelNameCache() *cacheSnapshot[string] {
	return openCacheFor[string](c, "channel-names", cacheTTLOf(c.cache).ChannelNames, nil)
}

func (c *Client) channelCache() *cacheSnapshot[CompactChannel] {
	return openCacheFor(c, "channels", cacheTTLOf(c.cache).Channels, validChannel)
}

func (c *Client) cachedChannelID(name string) (string, bool) {
	return c.channelNameCache().get(strings.ToLower(name))
}

func (c *Client) cacheChannelID(name, id string) {
	cacheSet(c.channelNameCache(), strings.ToLower(name), id, name != "" && id != "")
}

// warmChannelCache records channels a list command already fetched into the
// entity store and the name→ID index, so channel completions and later
// name→ID lookups are populated without their own API calls. Batched (one
// save per store) and best-effort. DMs go in the entity store (for ID→meta)
// but not the name index (they have no stable name).
func (c *Client) warmChannelCache(channels []CompactChannel) {
	entity := c.channelCache()
	names := c.channelNameCache()
	for _, ch := range channels {
		if ch.ID == "" {
			continue
		}
		entity.set(ch.ID, ch)
		if ch.Name != "" && !ch.IsIM {
			names.set(strings.ToLower(ch.Name), ch.ID)
		}
	}
	entity.save()
	names.save()
}

func (c *Client) cachedChannel(channelID string) (CompactChannel, bool) {
	return c.channelCache().get(channelID)
}

// channelInfoCache holds the full raw conversations.info object per channel, on
// the short Get TTL — distinct from the channels entity store (compact, list-
// warmed, partial: no member count). `channel get` reads/writes this so it
// serves a complete record (and --full) from cache; completions/resolution
// keep using the entity store.
func (c *Client) channelInfoCache() *cacheSnapshot[map[string]any] {
	return openCacheFor[map[string]any](c, "channel-info", cacheTTLOf(c.cache).Get,
		func(_ string, raw map[string]any) bool { return getStr(raw, "id") != "" })
}

func (c *Client) cacheChannelInfo(channelID string, raw map[string]any) {
	cacheSet(c.channelInfoCache(), channelID, raw, channelID != "" && getStr(raw, "id") != "")
}

func (c *Client) cacheChannel(ch CompactChannel) {
	if ch.ID == "" {
		return
	}
	cacheSet(c.channelCache(), ch.ID, ch, true)
	// A known channel also fills the name index, so a later name lookup is free.
	if ch.Name != "" {
		c.cacheChannelID(ch.Name, ch.ID)
	}
}
