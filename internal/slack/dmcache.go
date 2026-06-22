package slack

import (
	"slices"
	"strings"
)

// The dm_channels cache maps a normalized set of user ids to the DM (or group
// DM) channel id conversations.open returns for them. Slack keeps that mapping
// permanent — a DM channel id never changes for a given set of members — so it
// caches like stable profile data, not volatile membership. Only the channel
// *id* is cached; message history is always fetched live.
//
// It is populated two ways, both side-effect-free: lazily when a DM is actually
// opened (OpenDMChannel), and at warm time from the already-open DM list
// (warmDMChannels, via conversations.list types=im). It must NEVER be warmed by
// calling conversations.open on users no DM exists with yet — that *creates* the
// DM as a side effect. So warming reads the existing-DM list only; it never
// opens one.

// dmChannelCacheKey normalizes a member set into its cache key (sorted, so the
// key is independent of the order users were named).
func dmChannelCacheKey(userIDs []string) string {
	ids := slices.Clone(userIDs)
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

func (c *Client) dmChannelsCache() *cacheSnapshot[string] {
	return openCacheFor[string](c, "dm_channels", cacheTTLOf(c.cache).DMChannels, nil)
}

func (c *Client) cachedDMChannel(userIDs []string) (string, bool) {
	key := dmChannelCacheKey(userIDs)
	if key == "" {
		return "", false
	}
	return c.dmChannelsCache().get(key)
}

func (c *Client) cacheDMChannel(userIDs []string, channelID string) {
	key := dmChannelCacheKey(userIDs)
	cacheSet(c.dmChannelsCache(), key, channelID, key != "" && channelID != "")
}
