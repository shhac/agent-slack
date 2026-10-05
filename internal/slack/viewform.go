package slack

import (
	"context"

	"github.com/shhac/agent-slack/internal/render"
)

// ViewSummary is a modal an app opened, reduced to what a caller needs to
// fill it in: its title and the input fields by label, with their current
// values.
type ViewSummary struct {
	ID     string      `json:"id"`
	Title  string      `json:"title,omitempty"`
	Fields []ViewField `json:"fields,omitempty"`
	// Submitted reports whether the view was filled in and accepted.
	Submitted bool `json:"submitted"`
	// ResponseAction is Slack's answer to a submission ("clear", "update", …).
	ResponseAction string `json:"response_action,omitempty"`
	// Error is why a submission was not made or was rejected.
	Error string `json:"error,omitempty"`
	// Closed reports whether Slack accepted closing the view; false means it
	// may still be open on the user's other clients. A view accepted with
	// "clear" closed itself.
	Closed bool `json:"closed"`
}

// ViewField is one input block of a view.
type ViewField struct {
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Value    string   `json:"value,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// DescribeView summarises a view's input blocks.
func DescribeView(view map[string]any) ViewSummary {
	summary := ViewSummary{
		ID:    getStr(view, "id"),
		Title: render.TextObjectValue(view["title"]),
	}
	for _, in := range viewInputs(view) {
		field := ViewField{
			Title:    in.title,
			Type:     getStr(in.element, "type"),
			Required: !in.optional,
			Value:    entryDisplay(in.entry),
		}
		for _, o := range render.ElementOptions(in.element) {
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
