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
					// label-less field → the bare value on its own line.
					map[string]any{"type": "field", "field_type": "rich_text", "rich_text": richText("bare value")},
				},
			},
		},
	}

	got := renderWorkObject(a)
	if strings.Contains(got, "compact title") {
		t.Errorf("expanded layout must win over compact: %q", got)
	}
	for _, want := range []string{"Example issue title", "Status: Done", "Assignee: <@U0000000001>", "\nbare value"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "Empty") || strings.Contains(got, ": bare value") {
		t.Errorf("empty-value field must drop and label-less field must not gain a separator: %q", got)
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

	// Title without an external_url renders bare.
	a = compactWorkObject()
	woe = a["work_object_entity"].(map[string]any)
	delete(woe, "external_url")
	if got := renderWorkObject(a); !strings.HasPrefix(got, "Example issue title") {
		t.Errorf("title-only: %q", got)
	}
}

func TestRenderWorkObjectMalformedShapes(t *testing.T) {
	// Decoded API JSON is loosely specified; garbage shapes must degrade
	// gracefully (no panic, best-effort output), never render as empty
	// interface noise.
	richText := map[string]any{
		"type": "rich_text",
		"elements": []any{map[string]any{
			"type":     "rich_text_section",
			"elements": []any{map[string]any{"type": "text", "text": "Done"}},
		}},
	}
	mutate := func(f func(woe map[string]any)) map[string]any {
		a := compactWorkObject()
		f(a["work_object_entity"].(map[string]any))
		return a
	}
	cases := []struct {
		name    string
		a       map[string]any
		want    string
		exclude string
	}{
		{
			name: "non-record layout skipped in favor of a valid one",
			a: mutate(func(woe map[string]any) {
				woe["layouts"].(map[string]any)["expanded"] = "junk"
			}),
			want:    "Example issue title",
			exclude: "junk",
		},
		{
			name: "bare-string title ignored, url still surfaces",
			a: mutate(func(woe map[string]any) {
				woe["layouts"].(map[string]any)["compact"].(map[string]any)["title"] = "not-a-record"
			}),
			want:    "https://tracker.example.com/issue/EX-123/example-title",
			exclude: "not-a-record",
		},
		{
			name: "non-record field element skipped",
			a: mutate(func(woe map[string]any) {
				woe["layouts"].(map[string]any)["compact"].(map[string]any)["fields"] = map[string]any{
					"elements": []any{"junk-element", map[string]any{
						"type": "field", "field_type": "rich_text", "label": "Status", "rich_text": richText,
					}},
				}
			}),
			want:    "Status: Done",
			exclude: "junk-element",
		},
	}
	for _, tc := range cases {
		got := renderWorkObject(tc.a)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: missing %q in %q", tc.name, tc.want, got)
		}
		if strings.Contains(got, tc.exclude) {
			t.Errorf("%s: %q leaked into %q", tc.name, tc.exclude, got)
		}
	}
}

func TestWorkObjectSuppressesFallback(t *testing.T) {
	// The invariant this file exists for: a rendered work object counts as
	// content, so the attachment's notification fallback must not render
	// beside (or instead of) it.
	a := compactWorkObject()
	a["fallback"] = "notification fallback"
	got := RenderMessageContent(map[string]any{"text": "", "attachments": []any{a}})
	if strings.Contains(got, "notification fallback") {
		t.Errorf("fallback must be suppressed by work-object content: %q", got)
	}
	if !strings.Contains(got, "Example issue title") {
		t.Errorf("work-object content must render: %q", got)
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

func TestWorkObjectExpandedEndToEnd(t *testing.T) {
	// The expanded layout through the full Markdown pipeline: fields render
	// and the assignee mention converts to the bare-id form.
	a := compactWorkObject()
	woe := a["work_object_entity"].(map[string]any)
	woe["layouts"] = map[string]any{
		"expanded": map[string]any{
			"title":    map[string]any{"text": "Example issue title"},
			"subtitle": map[string]any{"text": "Issue EX-123 in TrackerApp"},
			"fields": map[string]any{
				"elements": []any{map[string]any{
					"type": "field", "field_type": "rich_text", "label": "Assignee",
					"rich_text": map[string]any{
						"type": "rich_text",
						"elements": []any{map[string]any{
							"type":     "rich_text_section",
							"elements": []any{map[string]any{"type": "user", "user_id": "U0000000001"}},
						}},
					},
				}},
			},
		},
	}
	got := RenderMessageContent(map[string]any{"text": "", "attachments": []any{a}})
	if !strings.Contains(got, "Assignee: @U0000000001") {
		t.Errorf("expanded field mention must reach final Markdown: %q", got)
	}
}
