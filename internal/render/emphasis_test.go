package render

import (
	"encoding/json"
	"strings"
	"testing"
)

// spans renders the styled runs of an outbound conversion as a compact string
// like `"foo" BOLD(bar) "baz"`, so a case reads as what Slack will show.
func spans(t *testing.T, blocks []RichTextBlock) string {
	t.Helper()
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var parts []string
	// Recursive: list items nest a section inside rich_text_list, so a
	// fixed-depth walk silently reports nothing for them.
	var walk func(any)
	walk = func(node any) {
		rec, ok := asRecord(node)
		if !ok {
			return
		}
		if kids := asSlice(rec["elements"]); len(kids) > 0 {
			for _, kid := range kids {
				walk(kid)
			}
			return
		}
		text := strings.ReplaceAll(str(rec["text"]), "\n", `\n`)
		if str(rec["type"]) == "link" {
			text = "LINK(" + str(rec["url"]) + ")"
		}
		if text == "" {
			return
		}
		style, _ := asRecord(rec["style"])
		var marks []string
		for _, k := range []string{"bold", "italic", "strike", "code"} {
			if truthy(style[k]) {
				marks = append(marks, strings.ToUpper(k))
			}
		}
		if len(marks) > 0 {
			parts = append(parts, strings.Join(marks, "+")+"("+text+")")
		} else {
			parts = append(parts, `"`+text+`"`)
		}
	}
	for _, blk := range decoded {
		walk(blk)
	}
	return strings.Join(parts, " ")
}

// Emphasis has two dialects with different rules, and the converter must not
// apply one dialect's rule to the other's text. Outbound we read CommonMark,
// where `*` MAY open mid-word and `_` may not. Inbound we read Slack mrkdwn,
// where a delimiter following a word character does not open at all — so text
// Slack displayed literally must come back literally.
func TestOutboundEmphasis(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"intraword bold uses asterisk runs", "foo**bar**baz", `"foo" BOLD(bar) "baz\n"`},
		{"intraword underscore stays literal", "snake_case_word", ""},
		{"plain bold", "**bold**", `BOLD(bold) "\n"`},
		{"single asterisk is italic and may open mid-word", "2*3 and 4*5", `"2" ITALIC(3 and 4) "5\n"`},
		{"code span keeps its markers literal", "`**x**`", `CODE(**x**) "\n"`},
		{"strike", "~~gone~~", `STRIKE(gone) "\n"`},
		{"bold containing italic nests", "**bold _and italic_**", `BOLD(bold ) BOLD+ITALIC(and italic) "\n"`},
		{"bold containing code nests", "**bold `code`**", `BOLD(bold ) BOLD+CODE(code) "\n"`},
		{"link inside bold", "**see [docs](https://x.invalid)**", `BOLD(see ) BOLD(LINK(https://x.invalid)) "\n"`},
	}
	for _, c := range cases {
		blocks, _ := RenderOutbound(c.input, false)
		if got := spans(t, blocks); got != c.want {
			t.Errorf("%s\n  input %q\n  got   %s\n  want  %s", c.name, c.input, got, c.want)
		}
	}
}

// An escaped marker must reach the reader as a literal character. Stripping the
// backslash is only half the job: the result then travels in the `text` field,
// which Slack parses as mrkdwn — so `*literal*` arrives bold unless the
// rich_text path carries it as inert text.
func TestOutboundEscapedMarkersStayLiteral(t *testing.T) {
	for _, input := range []string{`\*literal\*`, `a \*literal\* b`, `\~notstrike\~`} {
		blocks, fallback := RenderOutbound(input, false)
		if len(blocks) == 0 {
			t.Errorf("%q produced no blocks, so Slack re-parses the fallback %q and renders the markers as formatting",
				input, fallback)
			continue
		}
		if strings.Contains(spans(t, blocks), "BOLD") || strings.Contains(spans(t, blocks), "STRIKE") {
			t.Errorf("%q produced emphasis: %s", input, spans(t, blocks))
		}
	}
}

func TestInboundEmphasisFollowsSlackRules(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"space-flanked bold converts", "a *bold* b", "a **bold** b"},
		{"space-flanked strike converts", "a ~gone~ b", "a ~~gone~~ b"},
		{"intraword asterisks are literal in Slack", "2*3 and 4*5", "2*3 and 4*5"},
		{"intraword tildes are literal in Slack", "a~b and c~d", "a~b and c~d"},
		{"a path is not emphasis", "src/*.go and lib/*.go", "src/*.go and lib/*.go"},
		{"code stays literal", "`:wave:` and `*x*`", "`:wave:` and `*x*`"},
		{"underscores untouched", "snake_case_word", "snake_case_word"},
	}
	for _, c := range cases {
		if got := MrkdwnToMarkdown(c.input, false); got != c.want {
			t.Errorf("%s\n  input %q\n  got   %q\n  want  %q", c.name, c.input, got, c.want)
		}
	}
}

// A styled rich_text element can span a newline — Slack emits one element for a
// bold run crossing a line break. Wrapping it whole produces `*a\nb*`, which
// the inbound regex then refuses (it excludes newlines), so the bold is lost
// AND two stray asterisks are injected into the body.
func TestMultilineStyledSpansSurviveRoundTrip(t *testing.T) {
	block := map[string]any{"type": "rich_text", "elements": []any{
		map[string]any{"type": "rich_text_section", "elements": []any{
			map[string]any{"type": "text", "text": "line one\nline two", "style": map[string]any{"bold": true}},
		}}}}
	msg := MessageSummary{ChannelID: "C0FAKE1", TS: "1700000010.000100", Blocks: []any{block}}
	got := ToCompactMessage(msg, CompactOptions{}).Content
	want := "**line one**\n**line two**"
	if got != want {
		t.Errorf("multiline bold\n  got  %q\n  want %q", got, want)
	}
}

// Trailing structure must not leak into output or accumulate on round trips.
func TestTrailingWhitespaceAndNewlines(t *testing.T) {
	for _, c := range []struct{ name, input, want string }{
		{"trailing newline trimmed", "hello\n", "hello"},
		{"trailing spaces trimmed", "hello   ", "hello"},
		{"internal blank line kept", "a\n\nb", "a\n\nb"},
		{"trailing newline after bold", "**hi**\n", "**hi**"},
	} {
		msg := MessageSummary{ChannelID: "C0FAKE1", TS: "1700000010.000100", Text: c.input}
		if got := ToCompactMessage(msg, CompactOptions{}).Content; got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Nesting is where the two scanners disagree most easily: a construct that
// parses standalone can behave differently inside a list item, a quote, or
// another emphasis run.
func TestNestedConstructs(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"bold inside a list item", "- item with **bold**", `"item with " BOLD(bold)`},
		{"code inside a list item", "- run `npm i`", `"run " CODE(npm i)`},
		{"link inside a list item", "- see [docs](https://x.invalid)", `"see " "LINK(https://x.invalid)"`},
		{"bold inside a quote", "> quoted **bold**", `"quoted " BOLD(bold)`},
		{"bold italic strike together", "***~~all~~***", `BOLD+ITALIC+STRIKE(all) "\n"`},
		{"code beats emphasis inside it", "`**not bold**`", `CODE(**not bold**) "\n"`},
		{"escaped marker inside bold", `**a\*b**`, `BOLD(a*b) "\n"`},
	}
	for _, c := range cases {
		blocks, _ := RenderOutbound(c.input, false)
		if got := spans(t, blocks); got != c.want {
			t.Errorf("%s\n  input %q\n  got   %s\n  want  %s", c.name, c.input, got, c.want)
		}
	}
}

// One document exercising every construct at once, through the full round trip
// a real message takes: Markdown in -> rich_text out -> read back as Markdown.
// Individually-correct pieces can still interact badly, and a single case that
// reads like a real message catches that where unit cases do not.
func TestKitchenSinkRoundTrip(t *testing.T) {
	input := strings.Join([]string{
		"**Deploy 4821** finished in _6m12s_ ~~again~~",
		"",
		"- ran `make test` and `make lint`",
		"- see [the run](https://ci.example.invalid/4821)",
		"  - nested: 2*3 stays literal only inbound",
		"1. first",
		"2. second",
		"",
		"> quoted **bold** and a \\*literal\\* marker",
		"",
		"```",
		"symbol = :wave:",
		"count = 2*3",
		"```",
		"",
		"tail with :wave: emoji and snake_case_word",
	}, "\n")

	blocks, fallback := RenderOutbound(input, false)
	if len(blocks) == 0 {
		t.Fatalf("kitchen sink produced no blocks; fallback = %q", fallback)
	}

	// Read it back the way a reader command would.
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	msg := MessageSummary{ChannelID: "C0FAKE1", TS: "1700000010.000100", Blocks: decoded}
	got := ToCompactMessage(msg, CompactOptions{}).Content

	for _, want := range []string{
		"**Deploy 4821**", // bold survives
		"_6m12s_",         // italic survives
		"~~again~~",       // strike survives
		"`make test`",     // inline code survives
		"[the run](https://ci.example.invalid/4821)", // labelled link survives
		"1. first",          // ordered list survives
		"2. second",         // and keeps its numbering
		"> quoted **bold**", // quote with nested emphasis
		"symbol = :wave:",   // emoji NOT substituted inside the fence
		"count = 2*3",       // arithmetic NOT emphasised inside the fence
		"👋",                 // emoji IS substituted outside it
		"snake_case_word",   // underscores never emphasise
	} {
		if !strings.Contains(got, want) {
			t.Errorf("round trip lost %q\n--- got ---\n%s", want, got)
		}
	}
	// The literal marker must survive as characters, not as emphasis.
	if !strings.Contains(got, `*literal*`) && !strings.Contains(got, `\*literal\*`) {
		t.Errorf("escaped marker lost\n--- got ---\n%s", got)
	}
}

// Slack's composer produces mailto: links and this CLI sends them, so reading
// one back as a raw <mailto:…|label> token leaves the caller with something no
// reader can use.
func TestMailtoLinksConvertInbound(t *testing.T) {
	cases := map[string]string{
		"<mailto:a@b.invalid|email me>": "[email me](mailto:a@b.invalid)",
		"<mailto:a@b.invalid>":          "mailto:a@b.invalid",
		"<https://x.invalid|site>":      "[site](https://x.invalid)",
	}
	for input, want := range cases {
		if got := MrkdwnToMarkdown(input, false); got != want {
			t.Errorf("MrkdwnToMarkdown(%q) = %q, want %q", input, got, want)
		}
	}
}

// Additive fields a reader needs: a thread root that says how big the thread
// is, and a file that carries a human label even when it has no filename.
func TestCompactCarriesReplyCountAndFileTitle(t *testing.T) {
	msg := MessageSummary{
		ChannelID:  "C0FAKE1",
		TS:         "1700000010.000100",
		Text:       "the question",
		ReplyCount: 12,
		Files:      []FileSummary{{ID: "F0FAKEFILE", Title: "Q3 Report", Mimetype: "application/pdf"}},
	}
	compact := ToCompactMessage(msg, CompactOptions{})
	if compact.ReplyCount != 12 {
		t.Errorf("reply_count = %d, want 12 — a reader cannot tell a two-reply aside from a long decision without it", compact.ReplyCount)
	}
	if len(compact.Files) != 1 || compact.Files[0].Title != "Q3 Report" {
		t.Errorf("files = %+v, want the title surfaced", compact.Files)
	}
	if compact.Files[0].Name != "" {
		t.Error("title must not masquerade as a filename")
	}
}

// A shortcode shown as sample syntax inside code must stay text — the same rule
// mention resolution already follows.
func TestInlineEmojiSkipsCodeSpans(t *testing.T) {
	resolve := func(name string) string {
		if name == "shipit" {
			return "IMG"
		}
