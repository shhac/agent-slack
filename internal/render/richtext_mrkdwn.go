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

// styledTokenElements maps a single-token inline element type to the field
// holding its id and the slackToken kind to emit. They all serialize the same
// way, so a new mention-like element is a one-line addition here.
var styledTokenElements = map[string]struct{ idKey, kind string }{
	"emoji":     {"name", "emoji"},
	"user":      {"user_id", "user"},
	"channel":   {"channel_id", "channel"},
	"usergroup": {"usergroup_id", "usergroup"},
	"broadcast": {"range", "broadcast"},
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

	// Single-token mention-like elements all serialize identically: pull the id,
	// emit its Slack token wrapped in the element's style.
	if spec, ok := styledTokenElements[str(el["type"])]; ok {
		if v := str(el[spec.idKey]); v != "" {
			return applyMrkdwnStyle(slackToken(spec.kind, v), el["style"])
		}
		return ""
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
		return applyMrkdwnStyle(slackLink(str(el["url"]), str(el["text"])), el["style"])
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
