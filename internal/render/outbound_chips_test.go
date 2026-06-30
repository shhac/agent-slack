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
	return UpgradeOutboundLinks(b, text, slackMarkdown, ws)
}

func linkChips(blocks []RichTextBlock) []InlineElement {
	var out []InlineElement
	for _, el := range inlineElems(blocks) {
		if el.Type == "link" && el.Truncated {
			out = append(out, el)
		}
	}
	return out
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

const (
	tExtURL   = "https://github.com/acme/widgets"
	tExtLabel = "github.com/acme/widgets"
)

func TestLinkChipMarkdownUnlabeled(t *testing.T) {
	chips := linkChips(upgrade("["+tExtURL+"]("+tExtURL+")", false, tWS))
	if len(chips) != 1 {
		t.Fatalf("want 1 link chip, got %d", len(chips))
	}
	if c := chips[0]; c.URL != tExtURL || c.Text != tExtLabel || !c.Truncated {
		t.Errorf("chip = %+v", c)
	}
}

func TestLinkChipMrkdwnAngleForcesBlocks(t *testing.T) {
	// <url> in mrkdwn produces no blocks on its own — the upgrade must force them.
	chips := linkChips(upgrade("<"+tExtURL+">", true, tWS))
	if len(chips) != 1 || chips[0].Text != tExtLabel {
		t.Fatalf("<url> should become a link chip: %+v", chips)
	}
}

func TestLinkChipLabeledPreserved(t *testing.T) {
	blocks := upgrade("[the repo]("+tExtURL+")", false, tWS)
	if len(linkChips(blocks)) != 0 {
		t.Error("a labeled link must stay a plain link, not a chip")
	}
	var ok bool
	for _, e := range inlineElems(blocks) {
		if e.Type == "link" && e.URL == tExtURL && e.Text == "the repo" && !e.Truncated {
			ok = true
		}
	}
	if !ok {
		t.Errorf("labeled link missing/altered: %+v", inlineElems(blocks))
	}
}

func TestLinkChipMrkdwnLabeledPreserved(t *testing.T) {
	if len(linkChips(upgrade("<"+tExtURL+"|the repo>", true, tWS))) != 0 {
		t.Error("<url|label> must stay a plain link")
	}
}

func TestLinkChipLabelHumanizing(t *testing.T) {
	cases := map[string]string{
		"https://example.com/":     "example.com",
		"http://example.com/foo":   "example.com/foo",
		"https://example.com/a/b/": "example.com/a/b",
		"HTTPS://example.com/Foo":  "example.com/Foo", // scheme match is case-insensitive; path case kept
	}
	for url, want := range cases {
		chips := linkChips(upgrade("["+url+"]("+url+")", false, tWS))
		if len(chips) != 1 || chips[0].Text != want {
			t.Errorf("%q: want label %q, got %+v", url, want, chips)
		}
	}
}

func TestLinkChipMultipleInOneMessage(t *testing.T) {
	blocks := upgrade("[https://a.example/x](https://a.example/x) then [https://b.example/y](https://b.example/y)", false, tWS)
	chips := linkChips(blocks)
	if len(chips) != 2 {
		t.Fatalf("want 2 link chips, got %d: %+v", len(chips), chips)
	}
	if chips[0].Text != "a.example/x" || chips[1].Text != "b.example/y" {
		t.Errorf("chip labels wrong/out of order: %+v", chips)
	}
}

func TestLinkChipInsideList(t *testing.T) {
	// The upgrade walk must recurse into rich_text_list items.
	if got := len(linkChips(upgrade("- [https://example.com/x](https://example.com/x)", false, tWS))); got != 1 {
		t.Errorf("a link in a bullet list should chip, got %d chips", got)
	}
}

func TestLinkChipNonWebSkipped(t *testing.T) {
	if len(linkChips(upgrade("<mailto:someone@example.com>", true, tWS))) != 0 {
		t.Error("a mailto link must not become a web link chip")
	}
}

func TestLinkChipBareURLNotChipped(t *testing.T) {
	// A bare URL in plain text is deliberately left alone (no autolink).
	if len(linkChips(upgrade("see "+tExtURL+" please", false, tWS))) != 0 {
		t.Error("a bare URL must not autolink into a chip")
	}
}

func TestLinkChipWithFormatting(t *testing.T) {
	blocks := upgrade("**ship** ["+tExtURL+"]("+tExtURL+")", false, tWS)
	if len(linkChips(blocks)) != 1 {
		t.Fatalf("chip should coexist with other formatting")
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

func TestLinkChipMessagePermalinkPrefersMention(t *testing.T) {
	blocks := upgrade("["+tPermalink+"]("+tPermalink+")", false, tWS)
	if len(mentions(blocks)) != 1 {
		t.Error("a same-workspace message permalink should become a message_mention")
	}
	if len(linkChips(blocks)) != 0 {
		t.Error("message_mention must win over a plain link chip")
	}
}

func TestLinkChipMessagePermalinkNoWorkspaceFallsBack(t *testing.T) {
	// Without workspace context there's no message_mention; the permalink still
	// renders as a plain link chip.
	blocks := upgrade("["+tPermalink+"]("+tPermalink+")", false, "")
	if len(mentions(blocks)) != 0 {
		t.Error("no workspace → no message_mention")
	}
	if len(linkChips(blocks)) != 1 {
		t.Errorf("no workspace → link chip fallback, got %+v", linkChips(blocks))
	}
}
