package slack

import (
	"context"
	"encoding/base64"
	"maps"
	"strconv"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// NextCursor extracts response_metadata.next_cursor from a Slack response.
func NextCursor(resp map[string]any) string {
	return getStr(getRec(resp, "response_metadata"), "next_cursor")
}

// pageByOffset returns the page of items starting at offset (already decoded
// from a cursor) limited to limit, plus the cursor for the next page ("" when
// the slice is exhausted). It owns the boundary arithmetic shared by every
// client-side (in-memory) list — emoji list/search and usergroup list — so the
// off-by-one cases live in one place instead of being re-derived per caller.
func pageByOffset[T any](items []T, offset, limit int) ([]T, string) {
	if offset >= len(items) {
		return nil, ""
	}
	end := min(offset+limit, len(items))
	next := ""
	if end < len(items) {
		next = encodeOffsetCursor(end)
	}
	return items[offset:end], next
}

// encodeOffsetCursor / decodeOffsetCursor mint and read the opaque pagination
// cursor for a local (in-memory) result set, mirroring how Slack-backed lists
// hand back an opaque next_cursor the caller passes to --cursor.
func encodeOffsetCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeOffsetCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err == nil {
		if n, cerr := strconv.Atoi(string(raw)); cerr == nil && n >= 0 {
			return n, nil
		}
	}
	return 0, agenterrors.New("invalid pagination cursor", agenterrors.FixableByAgent).
		WithHint("omit --cursor to start from the first page, or pass a next_cursor from a prior page")
}

// eachHistoryPage walks conversations.history newest-first: the `latest`
// cursor threads from the last message of each page, stopping on an empty
// page, a repeated cursor (no progress), or fn returning false. fn receives
// the page's messages plus the raw response (for has_more-style checks).
func eachHistoryPage(ctx context.Context, c *Client, params map[string]any, latest string, fn func(messages []map[string]any, resp map[string]any) (bool, error)) error {
	cursorLatest := latest
	for {
		page := make(map[string]any, len(params)+1)
		maps.Copy(page, params)
		if cursorLatest != "" {
			page["latest"] = cursorLatest
		}
		resp, err := c.API(ctx, "conversations.history", page)
		if err != nil {
			return err
		}
		messages := recItems(getArr(resp, "messages"))
		if len(messages) == 0 {
			return nil
		}
		cont, err := fn(messages, resp)
		if err != nil {
			return err
		}
		if !cont {
			return nil
		}
		next := getStr(messages[len(messages)-1], "ts")
		if next == "" || next == cursorLatest {
			return nil
		}
		cursorLatest = next
	}
}

// EachPage calls method repeatedly, threading Slack's cursor between calls,
// until fn returns false, fn errors, or no cursor remains. params is not
// mutated; a caller-supplied "cursor" param seeds the first page.
func EachPage(ctx context.Context, c *Client, method string, params map[string]any, fn func(resp map[string]any) (bool, error)) error {
	cursor := ""
	for {
		page := make(map[string]any, len(params)+1)
		maps.Copy(page, params)
		if cursor != "" {
			page["cursor"] = cursor
		}
		resp, err := c.API(ctx, method, page)
		if err != nil {
			return err
		}
		cont, err := fn(resp)
		if err != nil {
			return err
		}
		if !cont {
			return nil
		}
		cursor = NextCursor(resp)
		if cursor == "" {
			return nil
		}
	}
}
