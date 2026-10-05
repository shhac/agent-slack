package slack

// Primitives for a modal opened on the user's behalf — a workflow's form or
// an app's — shared by the workflow and message-action flows.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// isOpenedView reports an RTM frame announcing a view opened for this user.
func isOpenedView(frame map[string]any) bool {
	t := getStr(frame, "type")
	return t == "view_opened" || t == "view_push"
}

// fetchOpenedView fetches the authoritative view via views.get — the real
// client re-fetches after tripping rather than trusting the push payload,
// which can be a stub when several clients share the session. Best-effort:
// any failure falls back to the event's view.
func fetchOpenedView(ctx context.Context, c *Client, eventView map[string]any) map[string]any {
	resp, err := c.API(ctx, "views.get", map[string]any{"view_id": getStr(eventView, "id")})
	if err != nil {
		return eventView
	}
	view := getRec(resp, "view")
	if len(getArr(view, "blocks")) == 0 {
		return eventView
	}
	return view
}

// closeView closes a view that will not be submitted and reports whether
// Slack accepted the close. Left open, a view lingers on the user's other
// clients.
func closeView(ctx context.Context, c *Client, viewID string) bool {
	if viewID == "" {
		return false
	}
	if _, err := c.API(ctx, "views.close", map[string]any{"view_id": viewID}); err != nil {
		c.debugf("views.close %s failed: %v", viewID, err)
		return false
	}
	return true
}

// submitViewState sends a view's state. The body is returned uninterpreted:
// a validation failure is ok:true too, and each caller words it.
func submitViewState(ctx context.Context, c *Client, viewID string, state map[string]any) (map[string]any, error) {
	stateJSON, _ := json.Marshal(map[string]any{"values": state})
	return c.API(ctx, "views.submit", map[string]any{
		"view_id":      viewID,
		"client_token": clientToken(),
		"state":        string(stateJSON),
	})
}

// rejectedFields reads a views.submit body's field errors as "Title: error"
// pairs; rejected is false when Slack accepted the submission.
func rejectedFields(resp map[string]any, titlesByBlock map[string]string) (detail string, rejected bool) {
	errsByBlock := getRec(resp, "errors")
	if getStr(resp, "response_action") != "errors" && len(errsByBlock) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(errsByBlock))
	for _, blockID := range slices.Sorted(maps.Keys(errsByBlock)) {
		label := FirstNonEmpty(titlesByBlock[blockID], blockID)
		parts = append(parts, fmt.Sprintf("%s: %v", label, errsByBlock[blockID]))
	}
	return FirstNonEmpty(strings.Join(parts, "; "), "no field errors were reported"), true
}
