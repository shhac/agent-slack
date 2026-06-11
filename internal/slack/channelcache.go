package slack

import "strings"

// Channel resolution is the most-repeated awkward lookup: name→ID has no Slack
// API and falls back to paginating the whole workspace. Two per-workspace
// caches back it — a name→ID index and an ID→CompactChannel entity store —
// each a single-key open/get or open/set/save (channels are looked up one or
// two at a time per command, so re-reading the small file is cheap).

func (c *Client) cachedChannelID(name string) (string, bool) {
	snap := openCache[string](c.cache, "channel-names", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).ChannelNames, nil)
	return snap.get(strings.ToLower(name))
}

func (c *Client) cacheChannelID(name, id string) {
	if name == "" || id == "" {
		return
	}
	snap := openCache[string](c.cache, "channel-names", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).ChannelNames, nil)
	snap.set(strings.ToLower(name), id)
	snap.save()
}

func (c *Client) cachedChannel(channelID string) (CompactChannel, bool) {
	snap := openCache[CompactChannel](c.cache, "channels", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).Channels, func(id string, ch CompactChannel) bool { return ch.ID != "" })
	return snap.get(channelID)
}

func (c *Client) cacheChannel(ch CompactChannel) {
	if ch.ID == "" {
		return
	}
	snap := openCache[CompactChannel](c.cache, "channels", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).Channels, func(id string, ch CompactChannel) bool { return ch.ID != "" })
	snap.set(ch.ID, ch)
	snap.save()
	// A known channel also fills the name index, so a later name lookup is free.
	if ch.Name != "" {
		c.cacheChannelID(ch.Name, ch.ID)
	}
}
