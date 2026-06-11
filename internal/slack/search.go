package slack

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
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

// searchUserIDForFilter resolves @name/name to an ID for fallback scans,
// matching handle or display name. Returns "" when unknown (no error: the
// filter just won't match anything, like the TS).
func searchUserIDForFilter(ctx context.Context, c *Client, input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if render.IsUserID(trimmed) {
		return trimmed
	}
	name := strings.TrimPrefix(trimmed, "@")
	found := ""
	_ = EachPage(ctx, c, "users.list", map[string]any{"limit": 200}, func(resp map[string]any) (bool, error) {
		for _, m := range recItems(getArr(resp, "members")) {
			display := getStr(getRec(m, "profile"), "display_name")
			if getStr(m, "name") == name || display == name {
				if id := getStr(m, "id"); id != "" {
					found = id
					return false, nil
				}
			}
		}
		return true, nil
	})
	return found
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

		paging := getRec(container, "paging")
		if paging == nil {
			paging = getRec(container, "pagination")
		}
		if totalPages := int(getNum(paging, "pages")); totalPages > 0 {
			pages = totalPages
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
		if opts.Download {
			for id, res := range DownloadMessageFiles(ctx, c, []render.MessageSummary{full}, MessageDownloads{DestDir: opts.DownloadsDir, Warn: opts.Warn}) {
				downloaded[id] = res
			}
		}
		compact := render.ToCompactMessage(full, render.CompactOptions{MaxBodyChars: maxContentChars, DownloadedPaths: downloaded})
		if !PassesContentTypeFilter(compact, contentType) {
			continue
		}
		resolved = append(resolved, full)
		compact.ThreadTS = ""
		out = append(out, SearchMessageItem{CompactMessage: compact, Permalink: ref.permalink})
		if len(out) >= limit {
			break
		}
	}

	users := resolveSearchUsers(ctx, c, opts, resolved)
	return out, users, nil
}

func searchMessagesInChannels(ctx context.Context, c *Client, opts SearchOptions, limit, maxContentChars int, contentType ContentType) ([]SearchMessageItem, map[string]CompactUser, error) {
	channelIDs, err := resolveChannelIDs(ctx, c, opts.Channels)
	if err != nil {
		return nil, nil, err
	}
	queryLower := strings.ToLower(strings.TrimSpace(opts.Query))
	userID := ""
	if opts.User != "" {
		userID = searchUserIDForFilter(ctx, c, opts.User)
	}
	var afterSec, beforeSec int64 = -1, -1
	if opts.After != "" {
		if afterSec, err = dateToUnixSeconds(opts.After, false); err != nil {
			return nil, nil, err
		}
	}
	if opts.Before != "" {
		if beforeSec, err = dateToUnixSeconds(opts.Before, true); err != nil {
			return nil, nil, err
		}
	}

	downloaded := map[string]render.DownloadResult{}
	var matched []render.MessageSummary
	var out []SearchMessageItem

channels:
	for _, channelID := range channelIDs {
		cursorLatest := ""
		for {
			params := map[string]any{"channel": channelID, "limit": 200}
			if cursorLatest != "" {
				params["latest"] = cursorLatest
			}
			resp, herr := c.API(ctx, "conversations.history", params)
			if herr != nil {
				return nil, nil, herr
			}
			messages := recItems(getArr(resp, "messages"))
			if len(messages) == 0 {
				break
			}

			pastOldest := false
			for _, m := range messages {
				summary := SummaryFromRaw(channelID, m)
				if tsNum, perr := strconv.ParseFloat(summary.TS, 64); perr == nil {
					if beforeSec >= 0 && int64(tsNum) > beforeSec {
						continue
					}
					if afterSec >= 0 && int64(tsNum) < afterSec {
						pastOldest = true
						break
					}
				}
				if userID != "" && summary.User != userID {
					continue
				}
				content := render.RenderMessageContent(map[string]any{
					"text": summary.Text, "blocks": summary.Blocks, "attachments": summary.Attachments,
				})
				if queryLower != "" && !strings.Contains(strings.ToLower(content), queryLower) {
					continue
				}
				if opts.Download {
					for id, res := range DownloadMessageFiles(ctx, c, []render.MessageSummary{summary}, MessageDownloads{DestDir: opts.DownloadsDir, Warn: opts.Warn}) {
						downloaded[id] = res
					}
				}
				compact := render.ToCompactMessage(summary, render.CompactOptions{MaxBodyChars: maxContentChars, DownloadedPaths: downloaded})
				if !PassesContentTypeFilter(compact, contentType) {
					continue
				}
				matched = append(matched, summary)
				compact.ThreadTS = ""
				out = append(out, SearchMessageItem{CompactMessage: compact})
				if len(out) >= limit {
					break channels
				}
			}
			if pastOldest {
				break
			}

			next := getStr(messages[len(messages)-1], "ts")
			if next == "" || next == cursorLatest {
				break
			}
			cursorLatest = next
		}
	}

	users := resolveSearchUsers(ctx, c, opts, matched)
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

func searchFilesInChannels(ctx context.Context, c *Client, opts SearchOptions, limit int, contentType ContentType) ([]SearchFileItem, error) {
	channelIDs, err := resolveChannelIDs(ctx, c, opts.Channels)
	if err != nil {
		return nil, err
	}
	userID := ""
	if opts.User != "" {
		userID = searchUserIDForFilter(ctx, c, opts.User)
	}
	queryLower := strings.ToLower(strings.TrimSpace(opts.Query))

	var out []SearchFileItem
	for _, channelID := range channelIDs {
		page := 1
		for {
			params := map[string]any{"channel": channelID, "count": 100, "page": page}
			if userID != "" {
				params["user"] = userID
			}
			if opts.After != "" {
				tsFrom, derr := dateToUnixSeconds(opts.After, false)
				if derr != nil {
					return nil, derr
				}
				params["ts_from"] = tsFrom
			}
			if opts.Before != "" {
				tsTo, derr := dateToUnixSeconds(opts.Before, true)
				if derr != nil {
					return nil, derr
				}
				params["ts_to"] = tsTo
			}
			resp, ferr := c.API(ctx, "files.list", params)
			if ferr != nil {
				return nil, ferr
			}
			files := recItems(getArr(resp, "files"))
			if len(files) == 0 {
				break
			}
			for _, f := range files {
				title := strings.TrimSpace(firstNonEmpty(getStr(f, "title"), getStr(f, "name")))
				if queryLower != "" && !strings.Contains(strings.ToLower(title), queryLower) {
					continue
				}
				item, ok := downloadSearchFile(ctx, c, f, opts, contentType)
				if !ok {
					continue
				}
				out = append(out, item)
				if len(out) >= limit {
					return out, nil
				}
			}
			paging := getRec(resp, "paging")
			if paging == nil {
				paging = getRec(resp, "pagination")
			}
			pages := int(getNum(paging, "pages"))
			if pages == 0 {
				pages = int(getNum(paging, "page_count"))
			}
			if pages > 0 && page >= pages {
				break
			}
			page++
		}
	}
	return out, nil
}

func downloadSearchFile(ctx context.Context, c *Client, f map[string]any, opts SearchOptions, contentType ContentType) (SearchFileItem, bool) {
	mode := getStr(f, "mode")
	mimetype := getStr(f, "mimetype")
	if !passesFileContentTypeFilter(mode, mimetype, contentType) {
		return SearchFileItem{}, false
	}
	fileURL := firstNonEmpty(getStr(f, "url_private_download"), getStr(f, "url_private"))
	id := getStr(f, "id")
	if fileURL == "" || id == "" {
		return SearchFileItem{}, false
	}
	name := id
	if ext := InferFileExt(render.FileSummary{
		Mimetype: mimetype, Filetype: getStr(f, "filetype"),
		Name: getStr(f, "name"), Title: getStr(f, "title"),
	}); ext != "" {
		name += "." + ext
	}
	path, err := c.DownloadFile(ctx, DownloadOptions{URL: fileURL, DestDir: opts.DownloadsDir, PreferredName: name})
	if err != nil {
		_, _ = fmt.Fprintf(opts.Warn, "Warning: skipping file %s: %s\n", id, err.Error())
		return SearchFileItem{}, false
	}
	return SearchFileItem{
		Title:    strings.TrimSpace(firstNonEmpty(getStr(f, "title"), getStr(f, "name"))),
		Mimetype: mimetype,
		Mode:     mode,
		Path:     path,
	}, true
}

// PassesContentTypeFilter classifies a compact message by its files.
func PassesContentTypeFilter(m render.CompactMessage, contentType ContentType) bool {
	if contentType == "any" {
		return true
	}
	hasFiles := len(m.Files) > 0
	if contentType == "text" {
		return !hasFiles
	}
	if !hasFiles {
		return false
	}
	switch contentType {
	case "file":
		return true
	case "snippet":
		for _, f := range m.Files {
			if f.Mode == "snippet" {
				return true
			}
		}
		return false
	case "image":
		for _, f := range m.Files {
			if strings.HasPrefix(f.Mimetype, "image/") {
				return true
			}
		}
		return false
	}
	return true
}

func passesFileContentTypeFilter(mode, mimetype string, contentType ContentType) bool {
	switch contentType {
	case "any", "file":
		return true
	case "snippet":
		return mode == "snippet"
	case "image":
		return strings.HasPrefix(strings.ToLower(mimetype), "image/")
	case "text":
		return mimetype == "text/plain"
	}
	return true
}

func resolveChannelIDs(ctx context.Context, c *Client, channels []string) ([]string, error) {
	out := make([]string, 0, len(channels))
	for _, ch := range channels {
		id, err := ResolveChannelID(ctx, c, ch)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

func resolveSearchUsers(ctx context.Context, c *Client, opts SearchOptions, messages []render.MessageSummary) map[string]CompactUser {
	ids := render.CollectReferencedUserIDs(messages, false)
	if !opts.ResolveUsers && !opts.RefreshUsers {
		return nil
	}
	users := ResolveUsersByID(ctx, c, opts.WorkspaceURL, ids, ResolveUsersOptions{
		CacheDir:     opts.UserCacheDir,
		ForceRefresh: opts.RefreshUsers,
	})
	return ToReferencedUsers(ids, users)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
