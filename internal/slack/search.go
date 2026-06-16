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

const (
	ContentAny     ContentType = "any"
	ContentText    ContentType = "text"
	ContentFile    ContentType = "file"
	ContentSnippet ContentType = "snippet"
	ContentImage   ContentType = "image"
)

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
	Warn            io.Writer
	SlackMarkdown   bool
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
	Messages             []SearchMessageItem         `json:"messages,omitempty"`
	Files                []SearchFileItem            `json:"files,omitempty"`
	ReferencedUsers      map[string]CompactUser      `json:"referenced_users,omitempty"`
	ReferencedChannels   map[string]CompactChannel   `json:"referenced_channels,omitempty"`
	ReferencedUsergroups map[string]CompactUsergroup `json:"referenced_usergroups,omitempty"`
}

// searchRefs holds the resolved referenced entities for a set of search hits.
type searchRefs struct {
	users      map[string]CompactUser
	channels   map[string]CompactChannel
	usergroups map[string]CompactUsergroup
}

// Search runs message and/or file search. With --channel filters it falls
// back to scanning channel history / files.list directly, because Slack's
// search API misses recent messages and needs search:read scope.
func Search(ctx context.Context, c *Client, opts SearchOptions) (SearchResult, error) {
	// Normalize once, in place (opts is a value copy): every helper then
	// reads the corrected fields instead of threading shadow parameters.
	opts.Limit = clampInt(orDefault(opts.Limit, 20), 1, 200)
	opts.MaxContentChars = orDefault(opts.MaxContentChars, 4000)
	if opts.ContentType == "" {
		opts.ContentType = ContentAny
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
		var refs searchRefs
		if len(opts.Channels) > 0 {
			messages, refs, err = searchMessagesInChannels(ctx, c, opts)
		} else {
			messages, refs, err = searchMessagesViaAPI(ctx, c, opts, slackQuery)
		}
		if err != nil {
			return SearchResult{}, err
		}
		out.Messages = messages
		out.ReferencedUsers = refs.users
		out.ReferencedChannels = refs.channels
		out.ReferencedUsergroups = refs.usergroups
	}

	if opts.Kind == SearchFiles || opts.Kind == SearchAll {
		var files []SearchFileItem
		if len(opts.Channels) > 0 {
			files, err = searchFilesInChannels(ctx, c, opts)
		} else {
			files, err = searchFilesViaAPI(ctx, c, opts, slackQuery)
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

// matchRef is the channel/ts/permalink coordinate of one search hit.
type matchRef struct {
	channelID string
	ts        string
	permalink string
}

// searchMatchRefs extracts hit coordinates from raw search.messages matches.
// It encodes the response-shape quirks — ts may be blank, channel may arrive
// name-only (resolved via the injected resolver so this stays testable
// without a client) — and caps at limit.
func searchMatchRefs(matches []map[string]any, limit int, resolveChannel func(name string) (string, error)) ([]matchRef, error) {
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
				var err error
				channelID, err = resolveChannel(name)
				if err != nil {
					return nil, err
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
	return refs, nil
}

func searchMessagesViaAPI(ctx context.Context, c *Client, opts SearchOptions, slackQuery string) ([]SearchMessageItem, searchRefs, error) {
	matches, err := searchPaged(ctx, c, "search.messages", "messages", slackQuery, opts.Limit)
	if err != nil {
		return nil, searchRefs{}, err
	}
	if len(matches) == 0 {
		return []SearchMessageItem{}, searchRefs{}, nil
	}

	matchRefs, err := searchMatchRefs(matches, opts.Limit, func(name string) (string, error) {
		return ResolveChannelID(ctx, c, "#"+name)
	})
	if err != nil {
		return nil, searchRefs{}, err
	}

	downloaded := map[string]render.DownloadResult{}
	var resolved []render.MessageSummary
	var out []SearchMessageItem
	for _, ref := range matchRefs {
		full, ok := fetchSearchMessage(ctx, c, opts, ref)
		if !ok {
			continue
		}
		hit, ok := searchHit(ctx, c, opts, full, downloaded, ref.permalink)
		if !ok {
			continue
		}
		resolved = append(resolved, full)
		out = append(out, hit)
		if len(out) >= opts.Limit {
			break
		}
	}

	return out, resolveSearchRefs(ctx, c, opts, resolved), nil
}

// fetchSearchMessage resolves one search match to its full message. The
// match's permalink (when parseable) corrects the workspace and supplies the
// thread hint. Misses are expected — matches can point at since-deleted or
// inaccessible messages — so failures report !ok rather than an error.
func fetchSearchMessage(ctx context.Context, c *Client, opts SearchOptions, ref matchRef) (render.MessageSummary, bool) {
	msgRef := &render.MessageRef{WorkspaceURL: opts.WorkspaceURL, ChannelID: ref.channelID, MessageTS: ref.ts, Raw: ref.permalink}
	if parsed, err := render.ParseMessageURL(ref.permalink); err == nil {
		msgRef.WorkspaceURL = parsed.WorkspaceURL
		msgRef.ThreadTSHint = parsed.ThreadTSHint
	}
	full, err := FetchMessage(ctx, c, msgRef, false)
	if err != nil {
		return render.MessageSummary{}, false
	}
	return full, true
}

func searchFilesViaAPI(ctx context.Context, c *Client, opts SearchOptions, slackQuery string) ([]SearchFileItem, error) {
	matches, err := searchPaged(ctx, c, "search.files", "files", slackQuery, opts.Limit)
	if err != nil {
		return nil, err
	}
	var out []SearchFileItem
	for _, f := range matches {
		item, ok := downloadSearchFile(ctx, c, f, opts)
		if !ok {
			continue
		}
		out = append(out, item)
		if len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

// resolveSearchRefs expands the users, channels, and usergroups referenced by
// the search hits, mirroring message get/list resolution.
func resolveSearchRefs(ctx context.Context, c *Client, opts SearchOptions, messages []render.MessageSummary) searchRefs {
	if !opts.ResolveUsers && !opts.RefreshUsers {
		return searchRefs{}
	}
	policy := ResolveCacheThenFetch
	if opts.RefreshUsers {
		policy = ResolveBypassCache
	}
	refs := render.CollectReferencedIDs(messages, false)
	users, _ := ResolveUsersByID(ctx, c, refs.Users, policy)
	channels, _ := ResolveChannelsByID(ctx, c, refs.Channels, policy)
	usergroups, _ := ResolveUsergroupsByID(ctx, c, refs.Usergroups, policy)
	return searchRefs{
		users:      ToReferencedUsers(refs.Users, users),
		channels:   channels,
		usergroups: usergroups,
	}
}
