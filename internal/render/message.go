package render

import "strings"

// RenderMessageContent collapses a raw Slack message (decoded JSON) to one
// Markdown string. Priority: blocks (rich_text and Block Kit) + attachments,
// then legacy text.
func RenderMessageContent(msg any) string {
	m, _ := asRecord(msg)
	return renderContent(str(m["text"]), asSlice(m["blocks"]), asSlice(m["attachments"]))
}

func renderContent(text string, blocks, attachments []any) string {
	st := &renderState{seen: map[uintptr]bool{}}
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
		return strings.TrimSpace(MrkdwnToMarkdown(combined))
	}

	if t := strings.TrimSpace(text); t != "" {
		return strings.TrimSpace(MrkdwnToMarkdown(t))
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
			if text, ok := asRecord(b["text"]); ok {
				textType := str(text["type"])
				if textType == "mrkdwn" || textType == "plain_text" {
					out = append(out, str(text["text"]))
				}
			}
			for _, fAny := range asSlice(b["fields"]) {
				f, ok := asRecord(fAny)
				if !ok {
					continue
				}
				fieldType := str(f["type"])
				if fieldType == "mrkdwn" || fieldType == "plain_text" {
					out = append(out, str(f["text"]))
				}
			}
			// Buttons often carry the only URL (e.g. "View Progress").
			if accessory, ok := asRecord(b["accessory"]); ok && str(accessory["type"]) == "button" {
				if line := buttonLine(accessory); line != "" {
					out = append(out, line)
				}
			}
		case "actions":
			for _, elAny := range asSlice(b["elements"]) {
				el, ok := asRecord(elAny)
				if !ok || str(el["type"]) != "button" {
					continue
				}
				if line := buttonLine(el); line != "" {
					out = append(out, line)
				}
			}
		case "context":
			for _, elAny := range asSlice(b["elements"]) {
				el, ok := asRecord(elAny)
				if !ok {
					continue
				}
				elType := str(el["type"])
				if elType == "mrkdwn" || elType == "plain_text" {
					out = append(out, str(el["text"]))
				}
			}
		case "image":
			alt := str(b["alt_text"])
			url := str(b["image_url"])
			if url == "" {
				continue
			}
			if alt != "" {
				out = append(out, alt+": "+url)
			} else {
				out = append(out, url)
			}
		case "rich_text":
			if rich := richTextBlockToMrkdwn(b); strings.TrimSpace(rich) != "" {
				out = append(out, rich)
			}
		}
	}
	return strings.Join(out, "\n\n")
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
