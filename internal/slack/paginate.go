package slack

import (
	"context"
	"maps"
)

// NextCursor extracts response_metadata.next_cursor from a Slack response.
func NextCursor(resp map[string]any) string {
	return getStr(getRec(resp, "response_metadata"), "next_cursor")
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
