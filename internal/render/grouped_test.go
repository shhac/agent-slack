package render

import (
	"strings"
	"testing"
	"time"
)

func TestRenderGroupedSectioned(t *testing.T) {
	g := Grouped{
		Summary: "Unreads · 1 channel · 2 unread",
		Sections: []GroupSection{{
			Heading: "#eng · 2 unread",
			Items: []GroupItem{
				{Title: SpeakerLine("1782032540.000100", "Alice", "U123", "", TranscriptOptions{Loc: time.UTC}), Details: []string{"hello"}},
				{Title: SpeakerLine("1782032600.000200", "Bob", "U456", "3 replies", TranscriptOptions{Loc: time.UTC}), Details: []string{"bumping"}},
			},
		}},
	}
	got := RenderGrouped(g, TranscriptOptions{Loc: time.UTC})
	want := "──── Unreads · 1 channel · 2 unread ────\n" +
		"\n" +
		"#eng · 2 unread\n" +
		"  [2026-06-21 09:02] <Alice|U123>\n" +
		"    hello\n" +
		"  [2026-06-21 09:03] <Bob|U456> · 3 replies\n" +
		"    bumping\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderGroupedFlatWithLead(t *testing.T) {
	g := Grouped{
		Summary: "Drafts · 1",
		Sections: []GroupSection{{
			Items: []GroupItem{{
				Lead:    "saved 14:02",
				Title:   "D0123 → #eng",
				Details: []string{"deploy summary", "📎 2 files"},
			}},
		}},
	}
	got := RenderGrouped(g, TranscriptOptions{Loc: time.UTC})
	want := "──── Drafts · 1 ────\n" +
		"\n" +
		"saved 14:02\n" +
		"D0123 → #eng\n" +
		"  deploy summary\n" +
		"  📎 2 files\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderGroupedEmpty(t *testing.T) {
	got := RenderGrouped(Grouped{Summary: "Unreads · 0", Empty: "No unreads."}, TranscriptOptions{})
	if !strings.Contains(got, "No unreads.") {
		t.Errorf("empty render = %q", got)
	}
}

func TestSpeakerLineFallbacks(t *testing.T) {
	// Unknown id renders "unknown"; name falls back to id; with-ids appends ts.
	got := SpeakerLine("1782032600.000200", "", "U456", "", TranscriptOptions{Loc: time.UTC, WithIDs: true})
	if !strings.Contains(got, "<U456|U456>") || !strings.Contains(got, "⟨ts 1782032600.000200⟩") {
		t.Errorf("speaker line = %q", got)
	}
	if blank := SpeakerLine("bad-ts", "", "", "", TranscriptOptions{}); !strings.Contains(blank, "<unknown|unknown>") {
		t.Errorf("blank author = %q", blank)
	}
}
