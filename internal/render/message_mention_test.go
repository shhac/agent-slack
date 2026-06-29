package render

import (
	"strings"
	"testing"
)

const (
	tWS        = "https://acme.slack.com"
	tPermalink = "https://acme.slack.com/archives/C0EXAMPLE1/p1700000000000100"
	tChannel   = "C0EXAMPLE1"
	tTS        = "1700000000.000100"
)

// upgrade mirrors outboundTextAndBlocks: render the text, then upgrade.
func upgrade(text string, slackMarkdown bool, ws string) []RichTextBlock {
	b, _ := RenderOutbound(text, slackMarkdown)
	return UpgradeMessageMentions(b, text, slackMarkdown, ws)
}

func inlineElems(blocks []RichTextBlock) []InlineElement {
	var out []InlineElement
	var walk func(elems []any)
	walk = func(elems []any) {
		for _, e := range elems {
			switch el := e.(type) {
			case InlineElement:
				out = append(out, el)
			case RichTextElement:
				walk(el.Elements)
			}
		}
	}
	for _, b := range blocks {
		for _, sec := range b.Elements {
			walk(sec.Elements)
		}
	}
	return out
}

func mentions(blocks []RichTextBlock) []InlineElement {
	var out []InlineElement
	for _, el := range inlineElems(blocks) {
		if el.Type == "message_mention" {
			out = append(out, el)
		}
	}
	return out
}

func TestMessageMentionBareURL(t *testing.T) {
	ms := mentions(upgrade(tPermalink, false, tWS))
	if len(ms) != 1 {
		t.Fatalf("want 1 chip, got %d", len(ms))
	}
	if m := ms[0]; m.ChannelID != tChannel || m.MessageTS != tTS || m.ThreadTS != tTS || m.URL != tPermalink {
		t.Errorf("chip = %+v", m)
	}
}

func TestMessageMentionBareURLMidText(t *testing.T) {
	blocks := upgrade("heads up "+tPermalink+" before we get to it", false, tWS)
	if len(mentions(blocks)) != 1 {
		t.Fatalf("want 1 chip")
	}
	var before, after bool
	for _, e := range inlineElems(blocks) {
		if e.Type == "text" && strings.Contains(e.Text, "heads up") {
			before = true
		}
		if e.Type == "text" && strings.Contains(e.Text, "before we get to it") {
			after = true
		}
	}
	if !before || !after {
		t.Errorf("surrounding text not preserved: %+v", inlineElems(blocks))
	}
}

func TestMessageMentionLabeledLinkPreserved(t *testing.T) {
	blocks := upgrade("[see here]("+tPermalink+")", false, tWS)
	if len(mentions(blocks)) != 0 {
		t.Error("a labeled link must stay a link, not become a chip")
	}
	var sawLink bool
	for _, e := range inlineElems(blocks) {
		if e.Type == "link" && e.URL == tPermalink && e.Text == "see here" {
			sawLink = true
		}
	}
	if !sawLink {
		t.Errorf("labeled link element missing: %+v", inlineElems(blocks))
	}
}

func TestMessageMentionUnlabeledFormsBecomeChips(t *testing.T) {
	// [url](url) (label == url, so no label) and <url> both have no label.
	if len(mentions(upgrade("["+tPermalink+"]("+tPermalink+")", false, tWS))) != 1 {
		t.Error("[url](url) should become a chip")
	}
	if len(mentions(upgrade("<"+tPermalink+">", true, tWS))) != 1 {
		t.Error("<url> (mrkdwn) should become a chip")
	}
}

func TestMessageMentionCrossWorkspaceSkipped(t *testing.T) {
	other := "https://other-workspace.slack.com/archives/C0EXAMPLE1/p1700000000000100"
	if len(mentions(upgrade(other, false, tWS))) != 0 {
		t.Error("a different workspace must not become a chip")
	}
}

func TestMessageMentionNonMessageURLSkipped(t *testing.T) {
	for _, u := range []string{
		"https://acme.slack.com/archives/C0EXAMPLE1", // channel, no message
		"https://example.com/foo",                            // not Slack
	} {
		if len(mentions(upgrade(u, false, tWS))) != 0 {
			t.Errorf("%q must not become a chip", u)
		}
	}
}

func TestMessageMentionThreadReply(t *testing.T) {
	reply := "https://acme.slack.com/archives/C0EXAMPLE1/p1700000000000200?thread_ts=1700000000.000100&cid=C0EXAMPLE1"
	ms := mentions(upgrade(reply, false, tWS))
	if len(ms) != 1 {
		t.Fatalf("want 1 chip")
	}
	if ms[0].MessageTS != "1700000000.000200" || ms[0].ThreadTS != "1700000000.000100" {
		t.Errorf("reply chip ts/thread wrong: %+v", ms[0])
	}
}

func TestMessageMentionEmptyWorkspaceSkips(t *testing.T) {
	if len(mentions(upgrade(tPermalink, false, ""))) != 0 {
		t.Error("empty workspaceURL must skip the upgrade")
	}
}

func TestMessageMentionTrailingPunctuation(t *testing.T) {
	blocks := upgrade("see "+tPermalink+".", false, tWS)
	ms := mentions(blocks)
	if len(ms) != 1 || ms[0].URL != tPermalink {
		t.Fatalf("trailing '.' should be trimmed off the chip url: %+v", ms)
	}
	var sawDot bool
	for _, e := range inlineElems(blocks) {
		if e.Type == "text" && strings.Contains(e.Text, ".") {
			sawDot = true
		}
	}
	if !sawDot {
		t.Error("the trailing period should remain as text")
	}
}

func TestMessageMentionWithFormatting(t *testing.T) {
	blocks := upgrade("**important** "+tPermalink, false, tWS)
	if len(mentions(blocks)) != 1 {
		t.Error("chip should coexist with other formatting")
	}
	var bold bool
	for _, e := range inlineElems(blocks) {
		if e.Type == "text" && e.Style != nil && e.Style.Bold {
			bold = true
		}
	}
	if !bold {
		t.Errorf("bold style lost alongside the chip: %+v", inlineElems(blocks))
	}
}
