package render

import (
	"strconv"
	"strings"
)

// richTextBlockToMrkdwn flattens a rich_text block (decoded JSON) to mrkdwn.
// The output still contains Slack tokens (<url|label>, <@U…>, :emoji:) — the
// caller converts the combined message to Markdown in one final pass.
func richTextBlockToMrkdwn(block any) string {
	b, ok := asRecord(block)
	if !ok {
		return ""
	}
	var out []string
	for _, el := range asSlice(b["elements"]) {
		if txt := richTextElementToMrkdwn(el); strings.TrimSpace(txt) != "" {
			out = append(out, txt)
		}
	}
	return strings.Join(out, "\n\n")
}

func richTextElementToMrkdwn(elAny any) string {
	el, ok := asRecord(elAny)
	if !ok {
		return ""
	}

	joinChildren := func() string {
		var parts []string
		for _, child := range asSlice(el["elements"]) {
			parts = append(parts, richTextElementToMrkdwn(child))
		}
		return strings.Join(parts, "")
	}

	switch str(el["type"]) {
	case "rich_text_section":
		return joinChildren()

	case "rich_text_preformatted":
		text := joinChildren()
		if text == "" {
			return ""
		}
		return "```" + text + "```"

	case "rich_text_quote":
		text := strings.TrimSpace(joinChildren())
		if text == "" {
			return ""
		}
		return quoteMarkdown(text)

	case "rich_text_list":
		style := str(el["style"])
		var items []string
		num := 0
		for _, item := range asSlice(el["elements"]) {
			txt := strings.TrimSpace(richTextElementToMrkdwn(item))
			if txt == "" {
				continue
			}
			num++
			if style == "ordered" {
				items = append(items, strconv.Itoa(num)+". "+txt)
			} else {
				items = append(items, "- "+txt)
			}
		}
		return strings.Join(items, "\n")

	case "text":
		return applyMrkdwnStyle(str(el["text"]), el["style"])

	case "link":
		url := str(el["url"])
		text := str(el["text"])
		token := url
		if url == "" {
			token = text
		} else if text != "" {
			token = "<" + url + "|" + text + ">"
		}
		return applyMrkdwnStyle(token, el["style"])

	case "emoji":
		if name := str(el["name"]); name != "" {
			return applyMrkdwnStyle(slackToken("emoji", name), el["style"])
		}
		return ""

	case "user":
		if userID := str(el["user_id"]); userID != "" {
			return applyMrkdwnStyle(slackToken("user", userID), el["style"])
		}
		return ""

	case "channel":
		if channelID := str(el["channel_id"]); channelID != "" {
			return applyMrkdwnStyle(slackToken("channel", channelID), el["style"])
		}
		return ""

	case "usergroup":
		if id := str(el["usergroup_id"]); id != "" {
			return applyMrkdwnStyle(slackToken("usergroup", id), el["style"])
		}
		return ""

	case "broadcast":
		if r := str(el["range"]); r != "" {
			return applyMrkdwnStyle(slackToken("broadcast", r), el["style"])
		}
		return ""
	}

	return ""
}

// applyMrkdwnStyle wraps a token in mrkdwn emphasis per the element's style.
// Slack keeps style on links/mentions/emoji (not just text), so a styled link
// serializes as _<url|label>_ and round-trips back to a styled link element.
func applyMrkdwnStyle(text string, styleAny any) string {
	style, ok := asRecord(styleAny)
	if !ok || text == "" {
		return text
	}
	if truthy(style["code"]) {
		text = "`" + text + "`"
	}
	if truthy(style["bold"]) {
		text = "*" + text + "*"
	}
	if truthy(style["italic"]) {
		text = "_" + text + "_"
	}
	if truthy(style["strike"]) {
		text = "~" + text + "~"
	}
	if truthy(style["underline"]) {
		text = "__" + text + "__"
	}
	return text
}
