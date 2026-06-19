package slack

import (
	"context"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// CompactUsergroup is the token-lean usergroup (subteam) projection emitted by
// the usergroup commands. Channels and Groups are the group's *default*
// channels and subteams — members are auto-added to them — surfaced so a
// caller can decide where to post to reach the group; the CLI takes no opinion
// on which is best.
type CompactUsergroup struct {
	ID          string   `json:"id"`               // S…
	Handle      string   `json:"handle,omitempty"` // bare handle, no leading @
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	UserCount   int      `json:"user_count,omitempty"`
	Channels    []string `json:"channels,omitempty"` // prefs.channels: default channel ids
	Groups      []string `json:"groups,omitempty"`   // prefs.groups: default subteam ids
	IsExternal  bool     `json:"is_external,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"` // date_delete != 0
}

// ToCompactUsergroup shapes one raw usergroups.list subteam object.
func ToCompactUsergroup(g map[string]any) CompactUsergroup {
	prefs := getRec(g, "prefs")
	return CompactUsergroup{
		ID:          getStr(g, "id"),
		Handle:      getStr(g, "handle"),
		Name:        getStr(g, "name"),
		Description: getStr(g, "description"),
		UserCount:   int(getNum(g, "user_count")),
		Channels:    getStrArr(prefs, "channels"),
		Groups:      getStrArr(prefs, "groups"),
		IsExternal:  getBool(g, "is_external"),
		Disabled:    getNum(g, "date_delete") != 0,
	}
}

// ListUsergroupsOptions controls ListUsergroups.
type ListUsergroupsOptions struct {
	IncludeDisabled bool   // include groups whose date_delete != 0
	Limit           int    // page size; <=0 uses defaultUsergroupListLimit, capped at maxUsergroupListLimit
	Cursor          string // opaque offset cursor from a previous page
}

const (
	defaultUsergroupListLimit = 200
	maxUsergroupListLimit     = 1000
)

// ListUsergroups returns one page of the workspace's usergroups plus the cursor
// for the next (empty when exhausted). usergroups.list has no server pagination
// — it returns the whole set in one call — so the full set is fetched once
// (cached on the short List TTL, shared with get and the warm caches) and then
// sliced client-side with the same opaque offset cursor as channel/user/emoji
// lists. The slice order is the API's, stable within the cache window. Limit
// and Cursor are deliberately NOT part of the page-cache key (usergroupsPageKey)
// — every page reads the one cached full set.
func ListUsergroups(ctx context.Context, c *Client, opts ListUsergroupsOptions) ([]CompactUsergroup, string, error) {
	offset, err := decodeOffsetCursor(opts.Cursor)
	if err != nil {
		return nil, "", err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultUsergroupListLimit
	}
	limit = clampInt(limit, 1, maxUsergroupListLimit)

	pages := c.usergroupsPageCache()
	pageKey := usergroupsPageKey(opts)
	groups, ok := pages.get(pageKey)
	if !ok {
		groups, err = fetchUsergroups(ctx, c, opts.IncludeDisabled)
		if err != nil {
			return nil, "", err
		}
		pages.set(pageKey, groups)
		pages.save()
	}

	page, next := pageByOffset(groups, offset, limit)
	return page, next, nil
}

// GetUsergroup fetches one usergroup by id (S…) or @handle. Slack has no
// usergroups.info, so it serves a cached entity within the short Get window and
// otherwise filters the full usergroups.list (which also warms the caches).
func GetUsergroup(ctx context.Context, c *Client, input string) (CompactUsergroup, error) {
	id, err := ResolveUsergroupID(ctx, c, input)
	if err != nil {
		return CompactUsergroup{}, err
	}
	if id != "" {
		serve := openCacheFor[CompactUsergroup](c, "usergroup-entities", cacheTTLOf(c.cache).Get, validUsergroup)
		if g, ok := serve.get(id); ok {
			return g, nil
		}
	} else if c.usergroupsComplete() {
		// The handle didn't resolve and the set is complete → authoritative
		// not-found; don't re-enumerate.
		return CompactUsergroup{}, errUsergroupNotResolved(input)
	}
	// Include disabled so `get` resolves a deactivated group by id too.
	groups, err := fetchUsergroups(ctx, c, true)
	if err != nil {
		return CompactUsergroup{}, err
	}
	key := handleCacheKey(input)
	for _, g := range groups {
		if g.ID == id || (id == "" && handleCacheKey(g.Handle) == key) {
			return g, nil
		}
	}
	return CompactUsergroup{}, errUsergroupNotResolved(input)
}

// ListUsergroupMembers returns the user ids in a usergroup (usergroups.users.list).
// input is an id (S…) or @handle. includeDisabled returns members of a
// deactivated group too.
func ListUsergroupMembers(ctx context.Context, c *Client, input string, includeDisabled bool) ([]string, error) {
	id, err := ResolveUsergroupID(ctx, c, input)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errUsergroupNotResolved(input)
	}
	params := map[string]any{"usergroup": id}
	if includeDisabled {
		params["include_disabled"] = true
	}
	resp, err := c.API(ctx, "usergroups.users.list", params)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, u := range getArr(resp, "users") {
		if s, ok := u.(string); ok && s != "" {
			ids = append(ids, s)
		}
	}
	return ids, nil
}

// ResolveUsergroupID maps a usergroup id (S…), handle (@marketing), or bare
// handle to its subteam id. On a handle cache miss it fetches the whole
// usergroups.list once and warms every group, so later lookups are free.
// Returns "" with no error when no usergroup matches (a not-found must not be
// cached against the TTL).
func ResolveUsergroupID(ctx context.Context, c *Client, input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if render.IsUsergroupID(trimmed) {
		return trimmed, nil
	}
	key := handleCacheKey(trimmed)
	if key == "" {
		return "", nil
	}
	if id, ok := c.cachedUsergroupIDByHandle(key); ok {
		return id, nil
	}
	if c.usergroupsComplete() {
		return "", nil // authoritative: the complete set holds no such handle
	}
	groups, err := fetchUsergroups(ctx, c, true)
	if err != nil {
		return "", err
	}
	for _, g := range groups {
		if handleCacheKey(g.Handle) == key {
			return g.ID, nil
		}
	}
	return "", nil
}

// fetchUsergroups calls usergroups.list (always with counts) and warms both the
// entity store and the handle index. The shared fetch behind resolve/list/get.
func fetchUsergroups(ctx context.Context, c *Client, includeDisabled bool) ([]CompactUsergroup, error) {
	params := map[string]any{"include_count": true}
	if includeDisabled {
		params["include_disabled"] = true
	}
	resp, err := c.API(ctx, "usergroups.list", params)
	if err != nil {
		return nil, err
	}
	var groups []CompactUsergroup
	for _, g := range recItems(getArr(resp, "usergroups")) {
		cg := ToCompactUsergroup(g)
		if cg.ID == "" {
			continue
		}
		groups = append(groups, cg)
	}
	c.warmUsergroups(groups)
	if includeDisabled {
		// usergroups.list returns everything; with disabled groups included it's
		// the complete set, so a later @group miss is authoritative.
		c.markUsergroupsComplete()
	}
	return groups, nil
}

func errUsergroupNotResolved(input string) *agenterrors.APIError {
	return errResolveFailed("usergroup: "+strings.TrimSpace(input),
		"pass a usergroup ID (S…) or @handle — 'agent-slack usergroup list' shows usergroups")
}
