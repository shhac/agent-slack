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
	want := "──── 2026-06-21 (UTC) ────\n" +
		"[09:02:20] <Alice|U12345555>\n" +
		"  Hello?\n" +
		"\n" +
		"[09:03:20] <Bob|U87654321>\n" +
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
	// 09:02:20 UTC == 10:02:20 BST in summer: the zone rides the day separator,
	// the header carries the local clock.
	if !strings.Contains(got, "──── 2026-06-21 (BST) ────") {
		t.Errorf("expected BST day separator, got:\n%s", got)
	}
	if !strings.Contains(got, "[10:02:20] <Alice|U12345555>") {
		t.Errorf("expected BST clock in header, got:\n%s", got)
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
	// The reply renders as a tree leaf under the root, body aligned under it.
	if !strings.Contains(got, "\n└─ [09:03:20] <Bob|U87654321>\n   reply\n") {
		t.Errorf("reply should nest under root as a tree leaf, got:\n%s", got)
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

func TestRenderTranscriptAuthorlessSpeaker(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", Text: "ghost"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC})
	if !strings.Contains(got, "<unknown|unknown>") {
		t.Errorf("authorless message (no User, BotID, or BotName) should render <unknown|unknown>, got:\n%s", got)
	}
}

func TestRenderTranscriptEmptyBodyStillHasHeader(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Files: []FileSummary{{ID: "F1", Name: "a.png"}}}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if !strings.Contains(got, "[09:02:20] <Alice|U12345555>\n  [file: a.png]\n") {
		t.Errorf("file-only message should still get a header, got:\n%s", got)
	}
}

func TestRenderTranscriptDaySeparator(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Text: "day one"}},
		// +1 day, 2026-06-22.
		{Summary: MessageSummary{TS: "1782118940.000000", User: "U12345555", Text: "day two"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if !strings.Contains(got, "──── 2026-06-21 (UTC) ────") || !strings.Contains(got, "──── 2026-06-22 (UTC) ────") {
		t.Errorf("each day should open with its own separator, got:\n%s", got)
	}
	// A new day always breaks the run, so the second day repeats the speaker.
	if strings.Count(got, "<Alice|U12345555>") != 2 {
		t.Errorf("new day should re-show the speaker, got:\n%s", got)
	}
}

func TestRenderTranscriptSpeakerGrouping(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Text: "first"}},
		// +60s, same author → collapses under the first header.
		{Summary: MessageSummary{TS: "1782032600.000000", User: "U12345555", Text: "second"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if strings.Count(got, "<Alice|U12345555>") != 1 {
		t.Errorf("grouped run should show the speaker once, got:\n%s", got)
	}
	// Grouped messages are not blank-line separated; the second is header-only.
	if !strings.Contains(got, "  first\n[09:03:20]\n  second\n") {
		t.Errorf("second message should collapse under the first, got:\n%s", got)
	}
}

func TestRenderTranscriptGroupingBreaksPastWindow(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Text: "first"}},
		// +6 min (> 300s window) → a fresh header, blank-line separated.
		{Summary: MessageSummary{TS: "1782032900.000000", User: "U12345555", Text: "later"}},
	}
	got := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: nameResolver(map[string]string{"U12345555": "Alice"})})
	if strings.Count(got, "<Alice|U12345555>") != 2 {
		t.Errorf("a gap past the window should re-show the speaker, got:\n%s", got)
	}
	if !strings.Contains(got, "  first\n\n[") {
		t.Errorf("non-grouped messages should be blank-line separated, got:\n%s", got)
	}
}

func TestRenderTranscriptColor(t *testing.T) {
	msgs := []TranscriptMessage{
		{Summary: MessageSummary{TS: "1782032540.000000", User: "U12345555", Text: "hi"}},
	}
	resolver := nameResolver(map[string]string{"U12345555": "Alice"})
	plain := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: resolver})
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("default render must be free of ANSI codes (LLM path), got:\n%q", plain)
	}
	colored := RenderTranscript(msgs, TranscriptOptions{Loc: time.UTC, UserName: resolver, Color: true})
	if !strings.Contains(colored, "\x1b[") || !strings.Contains(colored, ansiReset) {
		t.Errorf("Color:true should emit ANSI styling, got:\n%q", colored)
	}
	// The display name itself carries the bold-cyan span.
	if !strings.Contains(colored, ansiName+"Alice"+ansiReset) {
		t.Errorf("speaker name should be styled, got:\n%q", colored)
	}
}
