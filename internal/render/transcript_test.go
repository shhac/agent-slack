package render

import (
	"strings"
	"testing"
	"time"
)

// ts 1782032540 == 2026-06-21 09:02:20 UTC, used for deterministic stamps.
func nameResolver(names map[string]string) func(string) string {
	return func(id string) string { return names[id] }
}

func TestRenderTranscriptBasic(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.314239", User: "U12345555", Text: "Hello?"}},
		{Summary: MessageSummary{TS: "1782032600.100000", User: "U87654321", Text: "Hi <@U12345555>"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{
		Loc:      time.UTC,
		UserName: nameResolver(map[string]string{"U12345555": "Alice", "U87654321": "Bob"}),
	})
	want := "[2026-06-21 @ 09:02:20 (UTC)] <Alice|U12345555>\n" +
		"  Hello?\n" +
		"\n" +
		"[2026-06-21 @ 09:03:20 (UTC)] <Bob|U87654321>\n" +
		"  Hi @Alice\n"
	if got != want {
		t.Errorf("transcript mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderTranscriptMultilineBodyIndented(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Text: "line one\nline two"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if !strings.Contains(got, "\n  line one\n  line two\n") {
		t.Errorf("every body line should be indented 2 spaces, got:\n%s", got)
	}
}

func TestRenderTranscriptTimezoneLabel(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.314239", User: "U12345555", Text: "Hello?"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: london, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	// 09:02:20 UTC == 10:02:20 BST in summer.
	if !strings.HasPrefix(got, "[2026-06-21 @ 10:02:20 (BST)] <Alice|U12345555>") {
		t.Errorf("expected BST header, got:\n%s", got)
	}
}

func TestRenderTranscriptWithIDsToggle(t *testing.T) {
	msg := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.314239", User: "U12345555", Text: "Hello?"}},
	}
	off := RenderTranscript(msg, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if strings.Contains(off, "ts ") || strings.Contains(off, "⟨") {
		t.Errorf("default should not show ts id, got:\n%s", off)
	}
	on := RenderTranscript(msg, TranscriptOptions{Loc: time.UTC, WithIDs: true, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if !strings.Contains(on, "<Alice|U12345555>  ⟨ts 1782032540.314239⟩") {
		t.Errorf("--with-ids should append the verbatim ts, got:\n%s", on)
	}
}

func TestRenderTranscriptThreadIndent(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Text: "root"}, Depth: 0},
		{Summary: MessageSummary{TS: "1782032600.000000", ThreadTS: "1782032540.000000", User: "U87654321", Text: "reply"}, Depth: 1},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice", "U87654321": "Bob"})})
	// The reply header is indented one level, its body two levels.
	if !strings.Contains(got, "\n  [2026-06-21 @ 09:03:20 (UTC)] <Bob|U87654321>\n    reply\n") {
		t.Errorf("reply should nest under root, got:\n%s", got)
	}
}

func TestRenderTranscriptLinkToProse(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Text: "see <https://example.com|the docs>"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if !strings.Contains(got, "see the docs (https://example.com)") {
		t.Errorf("link should render as prose, got:\n%s", got)
	}
}

func TestRenderTranscriptBotSpeaker(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", BotID: "B999", Text: "deploy done"}, BotName: "Deploybot"},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC})
	if !strings.Contains(got, "<Deploybot|app>") {
		t.Errorf("bot author should render <Name|app>, got:\n%s", got)
	}
}

func TestRenderTranscriptUnknownUserFallback(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U00UNKNOWN", Text: "hi"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(nil)})
	if !strings.Contains(got, "<U00UNKNOWN|U00UNKNOWN>") {
		t.Errorf("unknown user should fall back to bare id, got:\n%s", got)
	}
}

func TestRenderTranscriptEditedAndFilesAndReactions(t *testing.T) {
	msgs := []TranscriptMessage{
		{
			Summary: MessageSummary{
				TS:    "1782032540.000000",
				User:  "U12345555",
				Text:  "look",
				Files: []FileSummary{{ID: "F1", Name: "report.pdf"}},
				Reactions: []any{
					map[string]any{"name": "+1", "users": []any{"U87654321"}},
				},
			},
			Edited: true,
		},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice", "U87654321": "Bob"})})
	if !strings.Contains(got, "<Alice|U12345555> (edited)") {
		t.Errorf("edited tag missing:\n%s", got)
	}
	if !strings.Contains(got, "  [file: report.pdf]") {
		t.Errorf("file line missing:\n%s", got)
	}
	if !strings.Contains(got, "↳ 👍 Bob") {
		t.Errorf("reaction trailer missing:\n%s", got)
	}
}

func TestRenderTranscriptEmptyBodyStillHasHeader(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Files: []FileSummary{{ID: "F1", Name: "a.png"}}}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if !strings.HasPrefix(got, "[2026-06-21 @ 09:02:20 (UTC)] <Alice|U12345555>\n  [file: a.png]\n") {
		t.Errorf("file-only message should still get a header, got:\n%s", got)
	}
}
