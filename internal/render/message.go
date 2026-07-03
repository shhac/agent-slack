package render

import "strings"

// RenderMessageContent collapses a raw Slack message (decoded JSON) to one
// standard-Markdown string. Priority: blocks (rich_text and Block Kit) +
// attachments, then legacy text.
func RenderMessageContent(msg any) string {
	return RenderMessageContentDialect(msg, false)
}

// RenderMessageContentDialect is RenderMessageContent with an explicit dialect:
// slackMarkdown true keeps the native Slack mrkdwn instead of standard Markdown.
func RenderMessageContentDialect(msg any, slackMarkdown bool) string {
	m, _ := asRecord(msg)
	return renderContent(str(m["text"]), asSlice(m["blocks"]), asSlice(m["attachments"]), slackMarkdown)
}

func renderContent(text string, blocks, attachments []any, slackMarkdown bool) string {
	st := &renderState{}
	blockMd := strings.TrimSpace(mrkdwnFromBlocks(blocks))
	attMd := strings.TrimSpace(mrkdwnFromAttachments(attachments, st))

	var combined string
	switch {
	case blockMd != "" && attMd != "":
		combined = blockMd + "\n\n" + attMd
	case blockMd != "":
		combined = blockMd
	case attMd != "":
		combined = attMd
	}
	if combined != "" {
		return strings.TrimSpace(MrkdwnToMarkdown(combined, slackMarkdown))
	}

	if t := strings.TrimSpace(text); t != "" {
		return strings.TrimSpace(MrkdwnToMarkdown(t, slackMarkdown))
	}
	return ""
}

func mrkdwnFromBlocks(blocks []any) string {
	var out []string
	for _, item := range blocks {
		b, ok := asRecord(item)
		if !ok {
			continue
		}
		switch str(b["type"]) {
		case "section":
			out = append(out, sectionLines(b)...)
		case "actions":
			out = append(out, actionsLines(b)...)
		case "context":
			out = append(out, contextLines(b)...)
		case "image":
			if line := imageLine(b); line != "" {
				out = append(out, line)
			}
		case "rich_text":
			if rich := richTextBlockToMrkdwn(b); strings.TrimSpace(rich) != "" {
				out = append(out, rich)
			}
		}
	}
	return strings.Join(out, "\n\n")
}

// mrkdwnTextValue returns the text of a Block Kit text object when it is a
// mrkdwn or plain_text object; ok is false for any other shape (so callers skip
// button/image/etc. objects that don't carry displayable prose).
func mrkdwnTextValue(v any) (string, bool) {
	t, ok := asRecord(v)
	if !ok {
		return "", false
	}
	switch str(t["type"]) {
	case "mrkdwn", "plain_text":
		return str(t["text"]), true
	}
	return "", false
}

// sectionLines renders a section block: its text, each field, and an accessory
// button (buttons often carry the only URL, e.g. "View Progress").
func sectionLines(b map[string]any) []string {
	var out []string
	if text, ok := mrkdwnTextValue(b["text"]); ok {
		out = append(out, text)
	}
	for _, fAny := range asSlice(b["fields"]) {
		if text, ok := mrkdwnTextValue(fAny); ok {
			out = append(out, text)
		}
	}
	if accessory, ok := asRecord(b["accessory"]); ok && str(accessory["type"]) == "button" {
		if line := buttonLine(accessory); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// actionsLines renders an actions block's button elements.
func actionsLines(b map[string]any) []string {
	var out []string
	for _, elAny := range asSlice(b["elements"]) {
		el, ok := asRecord(elAny)
		if !ok || str(el["type"]) != "button" {
			continue
		}
		if line := buttonLine(el); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// contextLines renders a context block's mrkdwn/plain_text elements.
func contextLines(b map[string]any) []string {
	var out []string
	for _, elAny := range asSlice(b["elements"]) {
		if text, ok := mrkdwnTextValue(elAny); ok {
			out = append(out, text)
		}
	}
	return out
}

// imageLine renders an image block as `alt: url` (or the bare url), "" when it
// has no image_url.
func imageLine(b map[string]any) string {
	url := str(b["image_url"])
	if url == "" {
		return ""
	}
	if alt := str(b["alt_text"]); alt != "" {
		return alt + ": " + url
	}
	return url
}

func buttonLine(button map[string]any) string {
	url := str(button["url"])
	if url == "" {
		return ""
	}
	label := ""
	if text, ok := asRecord(button["text"]); ok {
		label = str(text["text"])
	}
	if label != "" {
		return label + ": " + url
	}
	return url
}
