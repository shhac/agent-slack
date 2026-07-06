package render

import (
	"strings"
	"testing"
)

// fabricated work-object attachment mirroring the read-back shape Slack sends
// for app link unfurls: no classic fields, everything under work_object_entity.
func compactWorkObject() map[string]any {
	return map[string]any{
		"id":       float64(1),
		"from_url": "https://tracker.example.com/issue/EX-123/example-title",
		"work_object_entity": map[string]any{
			"app_name":     "TrackerApp",
			"display_type": "Issue",
			"external_url": "https://tracker.example.com/issue/EX-123/example-title",
			"layouts": map[string]any{
				"compact": map[string]any{
					"layout_type":    "compact",
					"title":          map[string]any{"text": "Example issue title"},
					"subtitle":       map[string]any{"text": "Issue EX-123 in TrackerApp"},
					"header_title":   map[string]any{"text": "TrackerApp"},
					"hover_subtitle": map[string]any{"text": "View latest details"},
				},
			},
		},
	}
}

func TestRenderWorkObjectCompact(t *testing.T) {
	got := renderWorkObject(compactWorkObject())
	want := "<https://tracker.example.com/issue/EX-123/example-title|Example issue title>\nIssue EX-123 in TrackerApp"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "View latest details") || strings.Contains(got, "TrackerApp\n") {
		t.Errorf("app chrome must not render: %q", got)
	}
}

func TestRenderWorkObjectExpandedFields(t *testing.T) {
	richText := func(text string) map[string]any {
		return map[string]any{
			"type": "rich_text",
			"elements": []any{map[string]any{
				"type":     "rich_text_section",
				"elements": []any{map[string]any{"type": "text", "text": text}},
			}},
		}
	}
	a := compactWorkObject()
	woe := a["work_object_entity"].(map[string]any)
	woe["layouts"] = map[string]any{
		// expanded wins over compact when both are present.
		"compact": map[string]any{"title": map[string]any{"text": "compact title"}},
		"expanded": map[string]any{
			"title":    map[string]any{"text": "Example issue title"},
			"subtitle": map[string]any{"text": "Issue EX-123 in TrackerApp"},
			"fields": map[string]any{
				"elements": []any{
					map[string]any{
						"type": "field", "field_type": "rich_text", "label": "Status",
						"rich_text": richText("Done"),
					},
					map[string]any{
						"type": "field", "field_type": "rich_text", "label": "Assignee",
						"rich_text": map[string]any{
							"type": "rich_text",
							"elements": []any{map[string]any{
								"type":     "rich_text_section",
								"elements": []any{map[string]any{"type": "user", "user_id": "U0000000001"}},
							}},
						},
					},
					// empty value → the whole field line is dropped, label and all.
					map[string]any{"type": "field", "field_type": "rich_text", "label": "Empty", "rich_text": richText("")},
				},
			},
		},
	}

	got := renderWorkObject(a)
	if strings.Contains(got, "compact title") {
		t.Errorf("expanded layout must win over compact: %q", got)
	}
	for _, want := range []string{"Example issue title", "Status: Done", "Assignee: <@U0000000001>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "Empty") {
		t.Errorf("field with empty value must be dropped: %q", got)
	}
}

func TestRenderWorkObjectEdges(t *testing.T) {
	// Not a work object at all.
	if got := renderWorkObject(map[string]any{"text": "classic"}); got != "" {
		t.Errorf("non-work-object: %q", got)
	}

	// Unknown layout name still renders (sorted-first fallback).
	a := compactWorkObject()
	woe := a["work_object_entity"].(map[string]any)
	woe["layouts"] = map[string]any{
		"experimental": map[string]any{"title": map[string]any{"text": "Example issue title"}},
	}
	if got := renderWorkObject(a); !strings.Contains(got, "Example issue title") {
		t.Errorf("unknown layout: %q", got)
	}

	// No layouts → the external_url still surfaces.
	delete(woe, "layouts")
	if got := renderWorkObject(a); got != "https://tracker.example.com/issue/EX-123/example-title" {
		t.Errorf("layout-less: %q", got)
	}
}

func TestWorkObjectMessageRendersNonEmpty(t *testing.T) {
	// The observed failure: a bot message whose only content is work-object
	// unfurls must not collapse to empty content.
	msg := map[string]any{
		"text":        "",
		"attachments": []any{compactWorkObject()},
	}
	got := RenderMessageContent(msg)
	if !strings.Contains(got, "[Example issue title](https://tracker.example.com/issue/EX-123/example-title)") {
		t.Errorf("title must render as a Markdown link: %q", got)
	}
	if !strings.Contains(got, "Issue EX-123 in TrackerApp") {
		t.Errorf("subtitle must render: %q", got)
	}
}
