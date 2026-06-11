package slack

import (
	"context"
	"maps"
)

// NextCursor extracts response_metadata.next_cursor from a Slack response.
func NextCursor(resp map[string]any) string {
	return getStr(getRec(resp, "response_metadata"), "next_cursor")
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
