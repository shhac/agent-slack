package render

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return m
}

func TestRenderPrefersBlocks(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "Title only",
		"blocks": [
			{"type": "section", "text": {"type": "mrkdwn", "text": "*Hi*\n<https://example.com|View>"}}
		]
	}`)
	got := RenderMessageContent(msg)
	want := "*Hi*\n[View](https://example.com)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderFallsBackToAttachments(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "",
		"attachments": [
			{"pretext": "New release published", "title": "<https://example.com|Release>", "text": "Hello"}
		]
	}`)
	got := RenderMessageContent(msg)
	if !strings.Contains(got, "[Release](https://example.com)") {
		t.Errorf("missing release link in %q", got)
	}
}

func TestRenderSectionFieldsAndButtonURLs(t *testing.T) {
	msg := mustJSON(t, `{
		"blocks": [
			{
				"type": "section",
				"text": {"type": "mrkdwn", "text": "*Started*"},
				"accessory": {"type": "button", "text": {"type": "plain_text", "text": "View"}, "url": "https://example.com/run/1"}
			},
			{
				"type": "section",
				"fields": [
					{"type": "mrkdwn", "text": "*Total Tests:*\n1"},
					{"type": "mrkdwn", "text": "*Triggered By:*\nSCHEDULED"}
				]
			}
		]
	}`)
	got := RenderMessageContent(msg)
	for _, want := range []string{"*Total Tests:*\n1", "*Triggered By:*\nSCHEDULED", "View: https://example.com/run/1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestRenderAttachmentFields(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "",
		"attachments": [
			{"fields": [
				{"title": "Total Tests:", "value": "1"},
				{"title": "Triggered By:", "value": "SCHEDULED"}
			]}
		]
	}`)
	got := RenderMessageContent(msg)
	if !strings.Contains(got, "Total Tests:") || !strings.Contains(got, "Triggered By:") {
		t.Errorf("missing attachment fields in %q", got)
	}
}

func TestRenderForwardedWithAuthorAndSource(t *testing.T) {
	msg := mustJSON(t, `{
		"blocks": [
			{"type": "rich_text", "elements": [{"type": "rich_text_section", "elements": [{"type": "emoji", "name": "eyes"}]}]}
		],
		"attachments": [
			{
				"is_msg_unfurl": true,
				"is_share": true,
				"author_name": "Alice",
				"author_link": "https://example.slack.com/team/U111",
				"from_url": "https://example.slack.com/archives/C222/p333",
				"message_blocks": [
					{"message": {"blocks": [
						{"type": "rich_text", "elements": [
							{"type": "rich_text_section", "elements": [{"type": "text", "text": "Hello from Alice"}]}
						]}
					]}}
				],
				"text": "Hello from Alice"
			}
		]
	}`)
	got := RenderMessageContent(msg)
	for _, want := range []string{
		"👀",
		"[Alice](https://example.slack.com/team/U111)",
		"[original](https://example.slack.com/archives/C222/p333)",
		"> Hello from Alice",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestRenderForwardedAuthorOnly(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "",
		"attachments": [{"is_share": true, "author_name": "Bob", "text": "Some forwarded text"}]
	}`)
	got := RenderMessageContent(msg)
	if !strings.Contains(got, "Forwarded from Bob") || !strings.Contains(got, "> Some forwarded text") {
		t.Errorf("got %q", got)
	}
}

func TestRenderForwardedNoAuthor(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "",
		"attachments": [{
			"is_share": true,
			"from_url": "https://example.slack.com/archives/C222/p333",
			"text": "Anonymous forward"
		}]
	}`)
	got := RenderMessageContent(msg)
	for _, want := range []string{
		"Forwarded message",
		"[original](https://example.slack.com/archives/C222/p333)",
		"> Anonymous forward",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestRenderForwardedFileLinks(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "",
		"attachments": [{
			"is_share": true,
			"from_url": "https://example.slack.com/archives/C222/p333",
			"message_blocks": [{"message": {"text": "Forwarded with image"}}],
			"files": [{"name": "image.png", "permalink": "https://example.slack.com/files/U1/F1/image.png"}]
		}]
	}`)
	got := RenderMessageContent(msg)
	if !strings.Contains(got, "> Forwarded with image") {
		t.Errorf("missing forwarded text in %q", got)
	}
	if !strings.Contains(got, "> [image.png](https://example.slack.com/files/U1/F1/image.png)") {
		t.Errorf("missing quoted file link in %q", got)
	}
}

func TestRenderNestedForwardedAttachments(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "",
		"attachments": [{
			"is_share": true,
			"author_name": "Carol",
			"message_blocks": [{"message": {"attachments": [{
				"title": "Nested update",
				"title_link": "https://example.com/update",
				"text": "Deployment passed",
				"fields": [{"title": "Env", "value": "prod"}]
			}]}}]
		}]
	}`)
	got := RenderMessageContent(msg)
	for _, want := range []string{
		"Forwarded from Carol",
		"> [Nested update](https://example.com/update)",
		"> Deployment passed",
		"> Env",
		"> prod",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestRenderDeduplicatesForwardedBody(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "",
		"attachments": [{
			"is_share": true,
			"text": "Same content",
			"message_blocks": [{"message": {"text": "Same content"}}]
		}]
	}`)
	got := RenderMessageContent(msg)
	if n := strings.Count(got, "> Same content"); n != 1 {
		t.Errorf("got %d occurrences of forwarded body, want 1: %q", n, got)
	}
}

func TestRenderCyclicForwardedAttachments(t *testing.T) {
	shared := map[string]any{
		"is_share":    true,
		"author_name": "Loop User",
		"text":        "Cycle-safe text",
	}
	shared["message_blocks"] = []any{
		map[string]any{"message": map[string]any{"attachments": []any{shared}}},
	}
	msg := map[string]any{"text": "", "attachments": []any{shared}}

	got := RenderMessageContent(msg)
	if !strings.Contains(got, "Forwarded from Loop User") || !strings.Contains(got, "> Cycle-safe text") {
		t.Errorf("got %q", got)
	}
}

func TestRenderLinkUnfurlIsNotForwarded(t *testing.T) {
	msg := mustJSON(t, `{
		"text": "",
		"attachments": [{
			"from_url": "https://github.com/org/repo/pull/42",
			"title": "Fix login bug",
			"title_link": "https://github.com/org/repo/pull/42",
			"text": "This PR fixes the login flow"
		}]
	}`)
	got := RenderMessageContent(msg)
	if strings.Contains(got, "Forwarded") {
		t.Errorf("link unfurl rendered as forward: %q", got)
	}
	if !strings.Contains(got, "[Fix login bug](https://github.com/org/repo/pull/42)") {
		t.Errorf("missing title link in %q", got)
	}
	if !strings.Contains(got, "This PR fixes the login flow") {
		t.Errorf("missing text in %q", got)
	}
}

func TestRenderCombinesBlocksAndAttachments(t *testing.T) {
	msg := mustJSON(t, `{
		"blocks": [{"type": "section", "text": {"type": "mrkdwn", "text": "Main content"}}],
		"attachments": [{"pretext": "Bot notification", "text": "Details here"}]
	}`)
	got := RenderMessageContent(msg)
	for _, want := range []string{"Main content", "Bot notification", "Details here"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestRenderLegacyTextFallback(t *testing.T) {
	got := RenderMessageContent(mustJSON(t, `{"text": "plain <https://example.com|link> :rocket:"}`))
	want := "plain [link](https://example.com) 🚀"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderRichTextElements(t *testing.T) {
	msg := mustJSON(t, `{
		"blocks": [{"type": "rich_text", "elements": [
			{"type": "rich_text_section", "elements": [
				{"type": "text", "text": "bold", "style": {"bold": true}},
				{"type": "text", "text": " and "},
				{"type": "text", "text": "code", "style": {"code": true}},
				{"type": "text", "text": " for "},
				{"type": "user", "user_id": "U123"},
				{"type": "text", "text": " in "},
				{"type": "channel", "channel_id": "C456"},
				{"type": "link", "url": "https://example.com", "text": "site"}
			]},
			{"type": "rich_text_list", "style": "ordered", "elements": [
				{"type": "rich_text_section", "elements": [{"type": "text", "text": "first"}]},
				{"type": "rich_text_section", "elements": [{"type": "text", "text": "second"}]}
			]},
			{"type": "rich_text_quote", "elements": [{"type": "text", "text": "quoted"}]},
			{"type": "rich_text_preformatted", "elements": [{"type": "text", "text": "x := 1"}]}
		]}]
	}`)
	got := RenderMessageContent(msg)
	for _, want := range []string{
		// Bare <#C456> survives: the mrkdwn pass only rewrites labeled
		// channel tokens, matching the TS behaviour.
		"*bold* and `code` for @U123 in <#C456>[site](https://example.com)",
		"1. first\n2. second",
		"> quoted",
		"```x := 1```",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestRenderEmptyMessage(t *testing.T) {
	if got := RenderMessageContent(mustJSON(t, `{}`)); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := RenderMessageContent(nil); got != "" {
		t.Errorf("nil: got %q, want empty", got)
	}
}
