package render

import (
	"reflect"
	"strings"
)

// maxAttachmentDepth bounds recursion through nested/forwarded attachments;
// the seen-set already breaks cycles, this catches pathological fan-out.
const maxAttachmentDepth = 8

// renderState is shared down the attachment recursion: depth increments per
// nesting level, seen tracks attachment map identity to survive cyclic JSON.
type renderState struct {
	depth int
	seen  map[uintptr]bool
}

func (st *renderState) next() *renderState {
	return &renderState{depth: st.depth + 1, seen: st.seen}
}

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

func mrkdwnFromAttachments(attachments []any, st *renderState) string {
	if st.depth >= maxAttachmentDepth {
		return ""
	}

	var parts []string
	for _, item := range attachments {
		a, ok := asRecord(item)
		if !ok {
			continue
		}
		ptr := reflect.ValueOf(a).Pointer()
		if st.seen[ptr] {
			continue
		}
		st.seen[ptr] = true

		_, hasMessageBlocks := a["message_blocks"].([]any)
		isSharedMessage := truthy(a["is_share"]) || (truthy(a["is_msg_unfurl"]) && hasMessageBlocks)

		if isSharedMessage {
			chunk := []string{forwardHeader(a)}
			body := strings.TrimSpace(forwardedMessageBody(a, st))
			if body == "" {
				body = strings.TrimSpace(mrkdwnFromAttachments(asSlice(a["attachments"]), st.next()))
			}
			if body == "" {
				body = strings.TrimSpace(str(a["text"]))
			}
			if body != "" {
				chunk = append(chunk, quoteMarkdown(body))
			}
			parts = append(parts, strings.Join(chunk, "\n"))
			continue
		}

		var chunk []string
		if blocks := mrkdwnFromBlocks(asSlice(a["blocks"])); strings.TrimSpace(blocks) != "" {
			chunk = append(chunk, blocks)
		}
		if pretext := str(a["pretext"]); pretext != "" {
			chunk = append(chunk, pretext)
		}
		title := str(a["title"])
		titleLink := str(a["title_link"])
		switch {
		case titleLink != "" && title != "":
			chunk = append(chunk, "<"+titleLink+"|"+title+">")
		case title != "":
			chunk = append(chunk, title)
		case titleLink != "":
			chunk = append(chunk, titleLink)
		}
		if text := str(a["text"]); text != "" {
			chunk = append(chunk, text)
		}
		for _, fAny := range asSlice(a["fields"]) {
			f, ok := asRecord(fAny)
			if !ok {
				continue
			}
			fieldTitle := str(f["title"])
			value := str(f["value"])
			switch {
			case fieldTitle != "" && value != "":
				chunk = append(chunk, fieldTitle+"\n"+value)
			case fieldTitle != "":
				chunk = append(chunk, fieldTitle)
			case value != "":
				chunk = append(chunk, value)
			}
		}
		if fallback := str(a["fallback"]); len(chunk) == 0 && fallback != "" {
			chunk = append(chunk, fallback)
		}
		if nested := mrkdwnFromAttachments(asSlice(a["attachments"]), st.next()); strings.TrimSpace(nested) != "" {
			chunk = append(chunk, nested)
		}
		if len(chunk) > 0 {
			parts = append(parts, strings.Join(uniqueTexts(chunk), "\n"))
		}
	}
	return strings.Join(uniqueTexts(parts), "\n\n")
}

func forwardHeader(a map[string]any) string {
	authorName := str(a["author_name"])
	authorLink := str(a["author_link"])
	fromURL := str(a["from_url"])

	authorPart := authorName
	if authorName != "" && authorLink != "" {
		authorPart = "<" + authorLink + "|" + authorName + ">"
	}
	sourcePart := ""
	if fromURL != "" {
		sourcePart = "<" + fromURL + "|original>"
	}

	switch {
	case authorPart != "" && sourcePart != "":
		return "*Forwarded from " + authorPart + " | " + sourcePart + "*"
	case authorPart != "":
		return "*Forwarded from " + authorPart + "*"
	case sourcePart != "":
		return "*Forwarded message | " + sourcePart + "*"
	default:
		return "*Forwarded message*"
	}
}

func forwardedMessageBody(a map[string]any, st *renderState) string {
	topLevelFiles := strings.TrimSpace(fileMentions(asSlice(a["files"])))
	messageBlocks, ok := a["message_blocks"].([]any)
	if !ok {
		return topLevelFiles
	}

	var out []string
	for _, mbAny := range messageBlocks {
		mb, ok := asRecord(mbAny)
		if !ok {
			continue
		}
		message, ok := asRecord(mb["message"])
		if !ok {
			continue
		}
		content := strings.Join(uniqueTexts([]string{
			strings.TrimSpace(mrkdwnFromBlocks(asSlice(message["blocks"]))),
			strings.TrimSpace(mrkdwnFromAttachments(asSlice(message["attachments"]), st.next())),
			strings.TrimSpace(str(message["text"])),
			strings.TrimSpace(fileMentions(asSlice(message["files"]))),
		}), "\n\n")
		if content != "" {
			out = append(out, content)
		}
	}
	return strings.Join(uniqueTexts(append([]string{topLevelFiles}, out...)), "\n")
}

func fileMentions(files []any) string {
	var lines []string
	for _, fAny := range files {
		f, ok := asRecord(fAny)
		if !ok {
			continue
		}
		name := str(f["title"])
		if name == "" {
			name = str(f["name"])
		}
		if name == "" {
			name = "file"
		}
		url := str(f["permalink"])
		if url == "" {
			url = str(f["url_private_download"])
		}
		if url == "" {
			url = str(f["url_private"])
		}
		if url != "" {
			lines = append(lines, "<"+url+"|"+name+">")
			continue
		}
		lines = append(lines, name)
	}
	return strings.Join(uniqueTexts(lines), "\n")
}

func quoteMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}

// uniqueTexts trims, drops empties, and deduplicates while keeping order —
// forwarded messages often repeat the same content in text and message_blocks.
func uniqueTexts(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		text := strings.TrimSpace(value)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		out = append(out, text)
	}
	return out
}
