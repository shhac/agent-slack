package slack

import "fmt"

// Page caches: the channel/user list output keyed by the query, on the short
// List TTL. A workflow hammering the same `channel list`/`user list` within
// the window reuses the page instead of refetching. Bypassed by --no-cache /
// --refresh-cache via the cache mode. Entity warming still happens on a miss,
// so completions fill regardless.

func (c *Client) conversationsPageCache() *cacheSnapshot[ConversationsPage] {
	return openCacheFor[ConversationsPage](c, "conversations-pages", cacheTTLOf(c.cache).List, nil)
}

func (c *Client) usersPageCache() *cacheSnapshot[UsersPage] {
	return openCacheFor[UsersPage](c, "users-pages", cacheTTLOf(c.cache).List, nil)
}

func (c *Client) usergroupsPageCache() *cacheSnapshot[[]CompactUsergroup] {
	return openCacheFor[[]CompactUsergroup](c, "usergroups-pages", cacheTTLOf(c.cache).List, nil)
}

// conversationsPageKey identifies one channel-list query. opts.User is already
// a resolved U… id by the time it reaches ListConversations.
func conversationsPageKey(opts ConversationsOptions) string {
	return fmt.Sprintf("all=%t|user=%s|types=%s|limit=%d|cursor=%s",
		opts.All, opts.User, opts.Types, opts.Limit, opts.Cursor)
}

func usersPageKey(opts ListUsersOptions) string {
	return fmt.Sprintf("bots=%t|limit=%d|cursor=%s", opts.IncludeBots, opts.Limit, opts.Cursor)
}

func usergroupsPageKey(opts ListUsergroupsOptions) string {
	return fmt.Sprintf("disabled=%t", opts.IncludeDisabled)
}
