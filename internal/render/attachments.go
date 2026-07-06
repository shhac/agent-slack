package render

// Attachment rendering: legacy/file attachments plus the recursive
// shared/forwarded-message shape. Split from message.go (which owns the
// Block Kit path); the two meet only in renderContent.

import "strings"

// maxAttachmentDepth bounds recursion through nested/forwarded attachments.
// Decoded JSON is a tree (each object is a distinct map, never aliased), so
// there are no reference cycles to break — this depth cap is the sole guard,
// catching pathological fan-out and any self-referential map a caller builds
// by hand.
const maxAttachmentDepth = 8

// renderState is threaded down the attachment recursion; depth increments per
// nesting level so the recursion stays bounded.
type renderState struct {
	depth int
}

func (st *renderState) next() *renderState {
	return &renderState{depth: st.depth + 1}
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
		_, hasMessageBlocks := a["message_blocks"].([]any)
		if truthy(a["is_share"]) || (truthy(a["is_msg_unfurl"]) && hasMessageBlocks) {
			parts = append(parts, renderSharedAttachment(a, st))
		} else if chunk := renderNormalAttachment(a, st); chunk != "" {
			parts = append(parts, chunk)
		}
	}
	return strings.Join(uniqueTexts(parts), "\n\n")
}

// renderSharedAttachment renders a forwarded/shared message: a header line
// plus the quoted original, recovered from message_blocks, nested
// attachments, or the fallback text — in that order.
func renderSharedAttachment(a map[string]any, st *renderState) string {
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
	return strings.Join(chunk, "\n")
}

// renderNormalAttachment renders a classic attachment: blocks, pretext,
// title(+link), text, fields, work object, fallback, then any nested
// attachments.
func renderNormalAttachment(a map[string]any, st *renderState) string {
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
	if workObject := renderWorkObject(a); workObject != "" {
		chunk = append(chunk, workObject)
	}
	if fallback := str(a["fallback"]); len(chunk) == 0 && fallback != "" {
		chunk = append(chunk, fallback)
	}
	if nested := mrkdwnFromAttachments(asSlice(a["attachments"]), st.next()); strings.TrimSpace(nested) != "" {
		chunk = append(chunk, nested)
	}
	if len(chunk) == 0 {
		return ""
	}
	return strings.Join(uniqueTexts(chunk), "\n")
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
