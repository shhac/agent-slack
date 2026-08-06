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
	want := "**Hi**\n[View](https://example.com)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderUnderlineRichText(t *testing.T) {
	// Underline has no Slack mrkdwn syntax; the reverse renderer represents it as
	// our __underline__ Markdown extension.
	msg := mustJSON(t, `{
		"blocks": [
			{"type": "rich_text", "elements": [
				{"type": "rich_text_section", "elements": [
					{"type": "text", "text": "plain "},
					{"type": "text", "text": "under", "style": {"underline": true}}
				]}
			]}
		]
	}`)
	got := RenderMessageContent(msg)
	if got != "plain __under__" {
		t.Errorf("got %q, want %q", got, "plain __under__")
	}
}

func TestRenderRichTextUsergroupAndBroadcast(t *testing.T) {
	// These element types used to render to "" (silent drop); now they emit the
	// raw Slack token rather than vanishing.
	msg := mustJSON(t, `{
		"blocks": [
			{"type": "rich_text", "elements": [
				{"type": "rich_text_section", "elements": [
					{"type": "text", "text": "ping "},
					{"type": "usergroup", "usergroup_id": "S12345678"},
					{"type": "text", "text": " and "},
					{"type": "broadcast", "range": "here"}
				]}
			]}
		]
	}`)
	got := RenderMessageContent(msg)
	if got != "ping <!subteam^S12345678> and @here" {
		t.Errorf("got %q", got)
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
	for _, want := range []string{"**Total Tests:**\n1", "**Triggered By:**\nSCHEDULED", "View: https://example.com/run/1"} {
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
		// channel tokens.
		"**bold** and `code` for @U123 in <#C456>[site](https://example.com)",
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

// The layer's dominant failure mode was silent deletion: an element or block
// type this renderer does not enumerate produced nothing at all, and because
// some *other* block rendered, the raw `text` fallback did not fire either.
// Content simply disappeared, with no error and nothing in the output to show
// something had been there.
func TestUnknownBlocksAndElementsDegradeRatherThanVanish(t *testing.T) {
	cases := []struct {
		name   string
		blocks []any
		want   string
	}{{
		name: "header carries the headline of most app notifications",
		blocks: []any{
			map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": "Deploy failed"}},
			map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "service: api"}},
		},
		want: "Deploy failed\n\nservice: api",
	}, {
		name: "message_mention is the chip we ourselves send",
		blocks: []any{map[string]any{"type": "rich_text", "elements": []any{
			map[string]any{"type": "rich_text_section", "elements": []any{
				map[string]any{"type": "text", "text": "see "},
				map[string]any{"type": "message_mention", "url": "https://acme.slack.com/archives/C0FAKE1/p1700000010000100"},
			}}}}},
		want: "see https://acme.slack.com/archives/C0FAKE1/p1700000010000100",
	}, {
		name: "an unmodelled element uses the fallback Slack supplies",
		blocks: []any{map[string]any{"type": "rich_text", "elements": []any{
			map[string]any{"type": "rich_text_section", "elements": []any{
				map[string]any{"type": "text", "text": "due "},
				map[string]any{"type": "date", "timestamp": float64(1700000000), "fallback": "Nov 14, 2023"},
			}}}}},
		want: "due Nov 14, 2023",
	}, {
		name: "an image with no url still has its alt text",
		blocks: []any{map[string]any{"type": "image", "alt_text": "chart of errors",
			"slack_file": map[string]any{"id": "F0FAKEFILE"}}},
		want: "chart of errors: F0FAKEFILE",
	}}

	for _, c := range cases {
		msg := MessageSummary{ChannelID: "C0FAKE1", TS: "1700000010.000100", Blocks: c.blocks}
		if got := ToCompactMessage(msg, CompactOptions{}).Content; got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// Code spans are literal. Slack does not emojify inside them, and rewriting a
// Ruby symbol or a :param: placeholder corrupts content an agent may act on.
func TestCodeSpansAreLeftLiteral(t *testing.T) {
	cases := map[string]string{
		"use `:wave:` here":         "use `:wave:` here",
		":wave: outside":            "👋 outside",
		"```\nsymbol = :wave:\n```": "```\nsymbol = :wave:\n```",
		"`<@U12345ABCDE>` in code":  "`<@U12345ABCDE>` in code",
	}
	for input, want := range cases {
		if got := MrkdwnToMarkdown(input, false); got != want {
			t.Errorf("MrkdwnToMarkdown(%q) = %q, want %q", input, got, want)
		}
	}
}

// actions/context/image renderers had no test at all: replacing any of them
// with a no-op left the suite green. An app notification's only link is often
// the action button's URL, so losing it loses the reason the message was sent.
func TestActionsContextAndImageBlocksRender(t *testing.T) {
	msg := MessageSummary{ChannelID: "C0FAKE1", TS: "1700000010.000100", Blocks: []any{
		map[string]any{"type": "actions", "elements": []any{
			map[string]any{"type": "button",
				"text": map[string]any{"type": "plain_text", "text": "View run"},
				"url":  "https://ci.example.invalid/run/42"},
		}},
		map[string]any{"type": "context", "elements": []any{
			map[string]any{"type": "mrkdwn", "text": "triggered by deploy-bot"},
		}},
		map[string]any{"type": "image", "alt_text": "error rate", "image_url": "https://example.invalid/chart.png"},
	}}

	content := ToCompactMessage(msg, CompactOptions{}).Content
	for _, want := range []string{
		"View run", "https://ci.example.invalid/run/42", // the button and its URL
		"triggered by deploy-bot", // context prose
		"error rate", "https://example.invalid/chart.png",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("content %q is missing %q", content, want)
		}
	}
}

// Slack sends these flags as booleans or as 0/1, which is the entire reason
// truthy exists — and the non-bool cases were untested, so a coercion that
// treated every number as set went unnoticed.
//
// Note "0" and "false" are TRUE: this is JavaScript truthiness, where only the
// empty string is falsy. That is deliberate, and the surprising part worth
// pinning.
func TestTruthyCoercesSlackFlagShapes(t *testing.T) {
	truthyCases := []any{true, float64(1), "1", "true", "0", "false"}
	falsyCases := []any{false, float64(0), "", nil}
	for _, v := range truthyCases {
		if !truthy(v) {
			t.Errorf("truthy(%#v) = false, want true", v)
		}
	}
	for _, v := range falsyCases {
		if truthy(v) {
			t.Errorf("truthy(%#v) = true, want false", v)
		}
	}
}
