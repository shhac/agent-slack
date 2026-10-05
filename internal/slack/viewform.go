package slack

import (
	"context"

	"github.com/shhac/agent-slack/internal/render"
)

// ViewSummary is a modal an app opened, reduced to what a caller needs to
// fill it in: its title and the input fields by label.
type ViewSummary struct {
	ID     string      `json:"id"`
	Title  string      `json:"title,omitempty"`
	Fields []ViewField `json:"fields,omitempty"`
	// Closed reports whether Slack accepted closing the view; false means it
	// may still be open on the user's other clients.
	Closed bool `json:"closed"`
}

// ViewField is one input block of a view.
type ViewField struct {
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// DescribeView summarises a view's input blocks.
func DescribeView(view map[string]any) ViewSummary {
	summary := ViewSummary{
		ID:    getStr(view, "id"),
		Title: render.TextObjectValue(view["title"]),
	}
	for _, b := range recItems(getArr(view, "blocks")) {
		if getStr(b, "type") != "input" {
			continue
		}
		element := getRec(b, "element")
		field := ViewField{
			Title:    render.TextObjectValue(b["label"]),
			Type:     getStr(element, "type"),
			Required: !getBool(b, "optional"),
		}
		for _, o := range render.ElementOptions(element) {
			if label := render.TextObjectValue(o["text"]); label != "" {
				field.Options = append(field.Options, label)
			}
		}
		summary.Fields = append(summary.Fields, field)
	}
	return summary
}

// OpenedView is the authoritative copy of a view a press opened: views.get
// re-fetched, falling back to the pushed payload.
func OpenedView(ctx context.Context, c *Client, pushed map[string]any) map[string]any {
	return fetchOpenedView(ctx, c, getStr(pushed, "id"), pushed)
}

// CloseView closes a view the caller will not submit, so it does not linger
// on the user's other clients, and reports whether Slack accepted the close.
func CloseView(ctx context.Context, c *Client, viewID string) bool {
	if viewID == "" {
		return false
	}
	if _, err := c.API(ctx, "views.close", map[string]any{"view_id": viewID}); err != nil {
		c.debugf("views.close %s failed: %v", viewID, err)
		return false
	}
	return true
}
