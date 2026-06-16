package slack

import (
	"context"
	"strconv"
	"strings"

	"github.com/shhac/agent-slack/internal/render"
)

// channelScanFilter is the pure per-message predicate of the channel-scan
// fallback: date window, author, and case-insensitive content matching.
type channelScanFilter struct {
	queryLower string
	userID     string
	afterSec   int64 // -1 = unbounded
	beforeSec  int64 // -1 = unbounded
}

func newChannelScanFilter(ctx context.Context, c *Client, opts SearchOptions) (channelScanFilter, error) {
	f := channelScanFilter{
		queryLower: strings.ToLower(strings.TrimSpace(opts.Query)),
		afterSec:   -1,
		beforeSec:  -1,
	}
	if opts.User != "" {
		f.userID = searchUserIDForFilter(ctx, c, opts.User)
	}
	var err error
	if opts.After != "" {
		if f.afterSec, err = dateToUnixSeconds(opts.After, false); err != nil {
			return channelScanFilter{}, err
		}
	}
	if opts.Before != "" {
		if f.beforeSec, err = dateToUnixSeconds(opts.Before, true); err != nil {
			return channelScanFilter{}, err
		}
	}
	return f, nil
}

// match reports whether the message passes the filters; pastOldest signals
// that the newest-first scan has crossed the --after boundary and the
// channel needs no further pages.
func (f channelScanFilter) match(summary render.MessageSummary) (keep, pastOldest bool) {
	if tsNum, err := strconv.ParseFloat(summary.TS, 64); err == nil {
		if f.beforeSec >= 0 && int64(tsNum) > f.beforeSec {
			return false, false
		}
		if f.afterSec >= 0 && int64(tsNum) < f.afterSec {
			return false, true
		}
	}
	if f.userID != "" && summary.User != f.userID {
		return false, false
	}
	if f.queryLower != "" {
		content := render.RenderMessageContent(map[string]any{
			"text": summary.Text, "blocks": summary.Blocks, "attachments": summary.Attachments,
		})
		if !strings.Contains(strings.ToLower(content), f.queryLower) {
			return false, false
		}
	}
	return true, false
}

func searchMessagesInChannels(ctx context.Context, c *Client, opts SearchOptions) ([]SearchMessageItem, ReferencedEntities, error) {
	channelIDs, err := resolveChannelIDs(ctx, c, opts.Channels)
	if err != nil {
		return nil, ReferencedEntities{}, err
	}
	filter, err := newChannelScanFilter(ctx, c, opts)
	if err != nil {
		return nil, ReferencedEntities{}, err
	}

	downloaded := map[string]render.DownloadResult{}
	var matched []render.MessageSummary
	var out []SearchMessageItem

	full := false
	for _, channelID := range channelIDs {
		err := eachHistoryPage(ctx, c, map[string]any{"channel": channelID, "limit": 200}, "", func(messages []map[string]any, _ map[string]any) (bool, error) {
			for _, m := range messages {
				summary := SummaryFromRaw(channelID, m)
				keep, pastOldest := filter.match(summary)
				if pastOldest {
					return false, nil
				}
				if !keep {
					continue
				}
				hit, ok := searchHit(ctx, c, opts, summary, downloaded, "")
				if !ok {
					continue
				}
				matched = append(matched, summary)
				out = append(out, hit)
				if len(out) >= opts.Limit {
					full = true
					return false, nil
				}
			}
			return true, nil
		})
		if err != nil {
			return nil, ReferencedEntities{}, err
		}
		if full {
			break
		}
	}

	return out, resolveSearchRefs(ctx, c, opts, matched), nil
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

func searchFilesInChannels(ctx context.Context, c *Client, opts SearchOptions) ([]SearchFileItem, error) {
	channelIDs, err := resolveChannelIDs(ctx, c, opts.Channels)
	if err != nil {
		return nil, err
	}
	userID := ""
	if opts.User != "" {
		userID = searchUserIDForFilter(ctx, c, opts.User)
	}
	queryLower := strings.ToLower(strings.TrimSpace(opts.Query))

	// The user filter and date window don't vary by channel or page — resolve
	// them once (eagerly returning a bad-date error) into a template the page
	// loop clones, instead of re-parsing the dates on every request.
	baseParams := map[string]any{"count": 100}
	if userID != "" {
		baseParams["user"] = userID
	}
	if opts.After != "" {
		tsFrom, derr := dateToUnixSeconds(opts.After, false)
		if derr != nil {
			return nil, derr
		}
		baseParams["ts_from"] = tsFrom
	}
	if opts.Before != "" {
		tsTo, derr := dateToUnixSeconds(opts.Before, true)
		if derr != nil {
			return nil, derr
		}
		baseParams["ts_to"] = tsTo
	}

	var out []SearchFileItem
	for _, channelID := range channelIDs {
		page := 1
		for {
			params := map[string]any{"channel": channelID, "page": page}
			for k, v := range baseParams {
				params[k] = v
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
				item, ok := downloadSearchFile(ctx, c, f, opts)
				if !ok {
					continue
				}
				out = append(out, item)
				if len(out) >= opts.Limit {
					return out, nil
				}
			}
			if pages := totalPages(resp); pages > 0 && page >= pages {
				break
			}
			page++
		}
	}
	return out, nil
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
