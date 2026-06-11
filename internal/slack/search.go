package slack

import (
	"context"
	"io"
	"regexp"
	"strings"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

type SearchKind string

const (
	SearchMessages SearchKind = "messages"
	SearchFiles    SearchKind = "files"
	SearchAll      SearchKind = "all"
)

type ContentType string

// SearchOptions controls Search.
type SearchOptions struct {
	WorkspaceURL    string
	Query           string
	Kind            SearchKind
	Channels        []string
	User            string // @name, name, or U…
	After, Before   string // YYYY-MM-DD
	ContentType     ContentType
	Limit           int // default 20, clamped to [1, 200]
	MaxContentChars int // 0 → 4000, negative → unlimited
	Download        bool
	ResolveUsers    bool
	RefreshUsers    bool
	DownloadsDir    string
	UserCacheDir    string
	Warn            io.Writer
}

// SearchMessageItem is a compact message hit; thread_ts is dropped (the
// permalink or channel_id+ts chain into message get/list).
type SearchMessageItem struct {
	render.CompactMessage
	Permalink string `json:"permalink,omitempty"`
}

type SearchFileItem struct {
	Title    string `json:"title,omitempty"`
	Mimetype string `json:"mimetype,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Path     string `json:"path"`
}

type SearchResult struct {
	Messages        []SearchMessageItem    `json:"messages,omitempty"`
	Files           []SearchFileItem       `json:"files,omitempty"`
	ReferencedUsers map[string]CompactUser `json:"referenced_users,omitempty"`
}

// Search runs message and/or file search. With --channel filters it falls
// back to scanning channel history / files.list directly, because Slack's
// search API misses recent messages and needs search:read scope.
func Search(ctx context.Context, c *Client, opts SearchOptions) (SearchResult, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = 20
	}
	limit = clampInt(limit, 1, 200)
	maxContentChars := opts.MaxContentChars
	if maxContentChars == 0 {
		maxContentChars = 4000
	}
	contentType := opts.ContentType
	if contentType == "" {
		contentType = "any"
	}
	if opts.Warn == nil {
		opts.Warn = io.Discard
	}

	slackQuery, err := buildSearchQuery(ctx, c, opts)
	if err != nil {
		return SearchResult{}, err
	}

	out := SearchResult{}
	if opts.Kind == SearchMessages || opts.Kind == SearchAll {
		var messages []SearchMessageItem
		var users map[string]CompactUser
		if len(opts.Channels) > 0 {
			messages, users, err = searchMessagesInChannels(ctx, c, opts, limit, maxContentChars, contentType)
		} else {
			messages, users, err = searchMessagesViaAPI(ctx, c, opts, slackQuery, limit, maxContentChars, contentType)
		}
		if err != nil {
			return SearchResult{}, err
		}
		out.Messages = messages
		out.ReferencedUsers = users
	}

	if opts.Kind == SearchFiles || opts.Kind == SearchAll {
		var files []SearchFileItem
		if len(opts.Channels) > 0 {
			files, err = searchFilesInChannels(ctx, c, opts, limit, contentType)
		} else {
			files, err = searchFilesViaAPI(ctx, c, opts, slackQuery, limit, contentType)
		}
		if err != nil {
			return SearchResult{}, err
		}
		out.Files = files
	}
	return out, nil
}

var searchDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func validateSearchDate(s string) (string, error) {
	v := strings.TrimSpace(s)
	if !searchDateRe.MatchString(v) {
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "invalid date: %s (expected YYYY-MM-DD)", s)
	}
	return v, nil
}

func dateToUnixSeconds(date string, endOfDay bool) (int64, error) {
	v, err := validateSearchDate(date)
	if err != nil {
		return 0, err
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return 0, agenterrors.Newf(agenterrors.FixableByAgent, "invalid date: %s (expected YYYY-MM-DD)", date)
	}
	if endOfDay {
		return t.Add(24*time.Hour - time.Millisecond).Unix(), nil
	}
	return t.Unix(), nil
}

// buildSearchQuery assembles Slack search syntax: query + after:/before: +
// from:@name + in:#name (IDs resolve to names — search syntax wants names).
func buildSearchQuery(ctx context.Context, c *Client, opts SearchOptions) (string, error) {
	var parts []string
	if base := strings.TrimSpace(opts.Query); base != "" {
		parts = append(parts, base)
	}
	if opts.After != "" {
		v, err := validateSearchDate(opts.After)
		if err != nil {
			return "", err
		}
		parts = append(parts, "after:"+v)
	}
	if opts.Before != "" {
		v, err := validateSearchDate(opts.Before)
		if err != nil {
			return "", err
		}
		parts = append(parts, "before:"+v)
	}
	if opts.User != "" {
		if token := userTokenForSearch(ctx, c, opts.User); token != "" {
			parts = append(parts, token)
		}
	}
	for _, ch := range opts.Channels {
		if token := channelTokenForSearch(ctx, c, ch); token != "" {
			parts = append(parts, token)
		}
	}
	return strings.Join(parts, " "), nil
}

func userTokenForSearch(ctx context.Context, c *Client, user string) string {
	trimmed := strings.TrimSpace(user)
	if trimmed == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(trimmed, "@"); ok {
		return "from:@" + rest
	}
	if render.IsUserID(trimmed) {
		resp, err := c.API(ctx, "users.info", map[string]any{"user": trimmed})
		if err != nil {
			return ""
		}
		if name := strings.TrimSpace(getStr(getRec(resp, "user"), "name")); name != "" {
			return "from:@" + name
		}
		return ""
	}
	return "from:@" + trimmed
}

func channelTokenForSearch(ctx context.Context, c *Client, channel string) string {
	id, name := NormalizeChannelInput(channel)
	if id == "" {
		if name = strings.TrimSpace(name); name == "" {
			return ""
		}
		return "in:#" + name
	}
	resp, err := c.API(ctx, "conversations.info", map[string]any{"channel": id})
	if err != nil {
		return ""
	}
	if chName := strings.TrimSpace(getStr(getRec(resp, "channel"), "name")); chName != "" {
		return "in:#" + chName
	}
	return ""
}

// searchPaged pages search.messages / search.files (page-number pagination).
func searchPaged(ctx context.Context, c *Client, method, containerKey, query string, limit int) ([]map[string]any, error) {
	pageSize := clampInt(limit, 1, 100)
	var out []map[string]any
	page := 1
	pages := 1
	for {
		resp, err := c.API(ctx, method, map[string]any{
			"query":     query,
			"count":     pageSize,
			"page":      page,
			"highlight": false,
			"sort":      "timestamp",
			"sort_dir":  "desc",
		})
		if err != nil {
			return nil, err
		}
		container := getRec(resp, containerKey)
		matches := recItems(getArr(container, "matches"))
		out = append(out, matches...)

		if tp := totalPages(container); tp > 0 {
			pages = tp
		}

		if len(out) >= limit || len(matches) == 0 || page >= pages {
			break
		}
		page++
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// totalPages reads the page count from a paged container, which Slack
// reports as paging or pagination (with pages or page_count) depending on
// the method.
func totalPages(container map[string]any) int {
	paging := getRec(container, "paging")
	if paging == nil {
		paging = getRec(container, "pagination")
	}
	if pages := int(getNum(paging, "pages")); pages > 0 {
		return pages
	}
	return int(getNum(paging, "page_count"))
}

func searchMessagesViaAPI(ctx context.Context, c *Client, opts SearchOptions, slackQuery string, limit, maxContentChars int, contentType ContentType) ([]SearchMessageItem, map[string]CompactUser, error) {
	matches, err := searchPaged(ctx, c, "search.messages", "messages", slackQuery, limit)
	if err != nil {
		return nil, nil, err
	}
	if len(matches) == 0 {
		return []SearchMessageItem{}, nil, nil
	}

	type matchRef struct {
		channelID string
		ts        string
		permalink string
	}
	var refs []matchRef
	for _, m := range matches {
		ts := strings.TrimSpace(getStr(m, "ts"))
		if ts == "" {
			continue
		}
		channel := getRec(m, "channel")
		channelID := getStr(channel, "id")
		if channelID == "" {
			if name := getStr(channel, "name"); name != "" {
				channelID, err = ResolveChannelID(ctx, c, "#"+name)
				if err != nil {
					return nil, nil, err
				}
			}
		}
		if channelID == "" {
			continue
		}
		refs = append(refs, matchRef{channelID: channelID, ts: ts, permalink: getStr(m, "permalink")})
		if len(refs) >= limit {
			break
		}
	}

	downloaded := map[string]render.DownloadResult{}
	var resolved []render.MessageSummary
	var out []SearchMessageItem
	for _, ref := range refs {
		msgRef := &render.MessageRef{WorkspaceURL: opts.WorkspaceURL, ChannelID: ref.channelID, MessageTS: ref.ts, Raw: ref.permalink}
		if parsed, perr := render.ParseMessageURL(ref.permalink); perr == nil {
			msgRef.WorkspaceURL = parsed.WorkspaceURL
			msgRef.ThreadTSHint = parsed.ThreadTSHint
		}
		full, ferr := FetchMessage(ctx, c, msgRef, false)
		if ferr != nil {
			continue // matches can point at since-deleted or inaccessible messages
		}
		hit, ok := searchHit(ctx, c, opts, full, downloaded, maxContentChars, contentType, ref.permalink)
		if !ok {
			continue
		}
		resolved = append(resolved, full)
		out = append(out, hit)
		if len(out) >= limit {
			break
		}
	}

	users := resolveSearchUsers(ctx, c, opts, resolved)
	return out, users, nil
}

func searchFilesViaAPI(ctx context.Context, c *Client, opts SearchOptions, slackQuery string, limit int, contentType ContentType) ([]SearchFileItem, error) {
	matches, err := searchPaged(ctx, c, "search.files", "files", slackQuery, limit)
	if err != nil {
		return nil, err
	}
	var out []SearchFileItem
	for _, f := range matches {
		item, ok := downloadSearchFile(ctx, c, f, opts, contentType)
		if !ok {
			continue
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func resolveSearchUsers(ctx context.Context, c *Client, opts SearchOptions, messages []render.MessageSummary) map[string]CompactUser {
	if !opts.ResolveUsers && !opts.RefreshUsers {
		return nil
	}
	ids := render.CollectReferencedUserIDs(messages, false)
	users := ResolveUsersByID(ctx, c, opts.WorkspaceURL, ids, ResolveUsersOptions{
		CacheDir:     opts.UserCacheDir,
		ForceRefresh: opts.RefreshUsers,
	})
	return ToReferencedUsers(ids, users)
}
