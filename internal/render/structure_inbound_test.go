package render

import "testing"

func rtCell(elements ...any) map[string]any {
	return map[string]any{"type": "rich_text", "elements": []any{
		map[string]any{"type": "rich_text_section", "elements": elements},
	}}
}

func rtText(text string, style map[string]any) map[string]any {
	el := map[string]any{"type": "text", "text": text}
	if style != nil {
		el["style"] = style
	}
	return el
}

func headerBlock(level int, text string) map[string]any {
	b := map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": text, "emoji": true}}
	if level > 0 {
		b["level"] = float64(level)
	}
	return b
}

var dividerBlock = map[string]any{"type": "divider"}

func TestStructuralBlocksRenderAsMarkdown(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		blocks []any
		want   string
	}{{
		name: "composer headings keep their level and dividers survive between content",
		blocks: []any{
			headerBlock(1, "Hello"), headerBlock(2, "world"), headerBlock(3, "foo bar"),
			dividerBlock,
			map[string]any{"type": "rich_text", "elements": []any{
				map[string]any{"type": "rich_text_section", "elements": []any{rtText("exciting", nil)}},
			}},
		},
		want: "# Hello\n\n## world\n\n### foo bar\n\n---\n\nexciting",
	}, {
		name: "an app header with no level is the notification's title",
		blocks: []any{
			headerBlock(0, "Deploy failed"),
			map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "service: api"}},
		},
		want: "# Deploy failed\n\nservice: api",
	}, {
		name: "edge and repeated dividers carry nothing",
		blocks: []any{
			dividerBlock, headerBlock(2, "Top"), dividerBlock, dividerBlock,
			headerBlock(2, "Bottom"), dividerBlock,
		},
		want: "## Top\n\n---\n\n## Bottom",
	}, {
		name:   "a dividers-only message still falls back to its text",
		text:   "the real message",
		blocks: []any{dividerBlock, dividerBlock},
		want:   "the real message",
	}, {
		name: "checklists render as task-list items at any depth",
		blocks: []any{map[string]any{"type": "rich_text", "elements": []any{
			map[string]any{"type": "rich_text_list", "style": "bullet", "checklist": true, "elements": []any{
				map[string]any{"type": "rich_text_section", "checked": false, "elements": []any{rtText("todo", nil)}},
				map[string]any{"type": "rich_text_section", "checked": true, "elements": []any{rtText("done", nil)}},
			}},
			map[string]any{"type": "rich_text_list", "style": "bullet", "checklist": true, "indent": float64(1), "elements": []any{
				map[string]any{"type": "rich_text_section", "checked": true, "elements": []any{rtText("child", nil)}},
			}},
		}}},
		want: "- [ ] todo\n- [x] done\n    - [x] child",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderMessageContent(map[string]any{"text": tc.text, "blocks": tc.blocks})
			if got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestTableBlockRendersAsPipeTable(t *testing.T) {
	table := map[string]any{
		"type": "table",
		"column_settings": []any{
			map[string]any{"align": "left"}, map[string]any{}, map[string]any{"align": "right"},
		},
		"rows": []any{
			[]any{
				map[string]any{"type": "raw_text", "text": "Service"},
				map[string]any{"type": "raw_text", "text": "Owner"},
				map[string]any{"type": "raw_text", "text": "p95"},
			},
			[]any{
				rtCell(rtText("checkout", map[string]any{"bold": true})),
				rtCell(map[string]any{"type": "user", "user_id": "U0FAKE01"}),
				rtCell(map[string]any{"type": "link", "url": "https://example.com/p95", "text": "412 ms"}),
			},
			[]any{
				rtCell(rtText("a|b", nil)),
				rtCell(rtText("two\nlines", nil)),
				// A short row still renders every column.
			},
		},
	}
	msg := map[string]any{"blocks": []any{table}}

	wantMarkdown := "| Service | Owner | p95 |\n" +
		"| :--- | --- | ---: |\n" +
		"| **checkout** | @U0FAKE01 | [412 ms](https://example.com/p95) |\n" +
		"| a\\|b | two lines |  |"
	if got := RenderMessageContent(msg); got != wantMarkdown {
		t.Errorf("markdown:\n got %q\nwant %q", got, wantMarkdown)
	}

	// The pipe inside a mrkdwn link token is Slack syntax, not a cell break.
	wantMrkdwn := "| Service | Owner | p95 |\n" +
		"| :--- | --- | ---: |\n" +
		"| *checkout* | <@U0FAKE01> | <https://example.com/p95|412 ms> |\n" +
		"| a\\|b | two lines |  |"
	if got := RenderMessageContentDialect(msg, true); got != wantMrkdwn {
		t.Errorf("slack mrkdwn:\n got %q\nwant %q", got, wantMrkdwn)
	}
}

// Slack splits a styled span around a link, leaving runs with edge spaces;
// `*see *` is not mrkdwn emphasis, so the spaces belong outside the markers.
func TestStyledRunsAroundALinkStayStyled(t *testing.T) {
	bold := map[string]any{"bold": true}
	msg := map[string]any{"blocks": []any{map[string]any{"type": "rich_text", "elements": []any{
		map[string]any{"type": "rich_text_section", "elements": []any{
			rtText("Heading with a ", bold),
			map[string]any{"type": "link", "url": "https://example.com", "text": "link", "style": bold},
			rtText(" in it", bold),
		}},
	}}}}
	want := "**Heading with a** **[link](https://example.com)** **in it**"
	if got := RenderMessageContent(msg); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// Sections within one rich_text block end in their own newline; the paragraph
// gap between them must not double.
func TestSectionsInOneBlockKeepASingleGap(t *testing.T) {
	msg := map[string]any{"blocks": []any{map[string]any{"type": "rich_text", "elements": []any{
		map[string]any{"type": "rich_text_section", "elements": []any{rtText("first\n", nil)}},
		map[string]any{"type": "rich_text_section", "elements": []any{rtText("second\n", nil)}},
	}}}}
	if got := RenderMessageContent(msg); got != "first\n\nsecond" {
		t.Errorf("got %q", got)
	}
}

func TestColorElementShowsItsHex(t *testing.T) {
	msg := map[string]any{"blocks": []any{map[string]any{"type": "rich_text", "elements": []any{
		map[string]any{"type": "rich_text_section", "elements": []any{
			rtText("brand ", nil),
			map[string]any{"type": "color", "value": "#FF5A1F"},
		}},
	}}}}
	if got := RenderMessageContent(msg); got != "brand #FF5A1F" {
		t.Errorf("got %q", got)
	}
}
