package render

import (
	"strings"
	"time"
)

// Grouped is sectioned, human-readable output in the transcript visual
// language: an optional `──── summary ────` divider, then sections of
// title+detail blocks. It backs --format transcript for the list/group commands
// (unreads grouped by channel, later by state, drafts, and the entity digests)
// the way RenderTranscript backs the conversation reads.
type Grouped struct {
	// Summary is the top divider label (e.g. "Unreads · 3 channels · 12 unread").
	// Empty omits the divider.
	Summary string
	// Sections render in order; a section with no items is skipped.
	Sections []GroupSection
	// Empty is shown (dimmed) when no section has any item (e.g. "No unreads.").
	Empty string
}

// GroupSection is one labelled group of items. A blank Heading renders the
// items flat at the left margin (used by the flat digests like drafts).
type GroupSection struct {
	Heading string
	Items   []GroupItem
}

// GroupItem is one block: an optional dim Lead context line, a Title headline,
// then indented Detail lines. Title/Detail are rendered verbatim, so callers
// paint them (e.g. via SpeakerLine) before handing them over.
type GroupItem struct {
	Lead    string
	Title   string
	Details []string
}

// RenderGrouped renders g as text. Sections are separated by a blank line; a
// section heading sits at the margin with its items indented two spaces (their
// details four). Headless sections render items at the margin.
func RenderGrouped(g Grouped, opts TranscriptOptions) string {
	var b strings.Builder
	if g.Summary != "" {
		b.WriteString(paint(opts.Color, ansiDim, "──── "+g.Summary+" ────"))
		b.WriteString("\n")
	}

	total := 0
	for _, s := range g.Sections {
		total += len(s.Items)
	}
	if total == 0 {
		empty := g.Empty
		if empty == "" {
			empty = "(nothing to show)"
		}
		if g.Summary != "" {
			b.WriteString("\n")
		}
		b.WriteString(paint(opts.Color, ansiDim, empty))
		b.WriteString("\n")
		return b.String()
	}

	for _, s := range g.Sections {
		if len(s.Items) == 0 {
			continue
		}
		b.WriteString("\n")
		itemIndent := ""
		if s.Heading != "" {
			b.WriteString(paint(opts.Color, ansiName, s.Heading))
			b.WriteString("\n")
			itemIndent = transcriptIndent
		}
		detailIndent := itemIndent + transcriptIndent
		for _, it := range s.Items {
			if it.Lead != "" {
				b.WriteString(itemIndent)
				b.WriteString(paint(opts.Color, ansiDim, it.Lead))
				b.WriteString("\n")
			}
			b.WriteString(itemIndent)
			b.WriteString(it.Title)
			b.WriteString("\n")
			for _, d := range it.Details {
				b.WriteString(detailIndent)
				b.WriteString(d)
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// Emphasize paints s as a heading/identifier (bold cyan) when color is on; it
// lets callers build GroupItem titles (draft ids, channel/user names) in the
// same visual key as section headings and speaker names.
func Emphasize(s string, color bool) string { return paint(color, ansiName, s) }

// Dim paints s as secondary metadata (dim) when color is on — for the trailing
// detail of a digest title (scheduled time, counts) and ad-hoc dim fragments.
func Dim(s string, color bool) string { return paint(color, ansiDim, s) }

// SpeakerLine formats a conversation-style headline — `[<time>] <Name|id>` plus
// an optional ` · note` suffix and the `⟨ts …⟩` id region — for a GroupItem
// Title, using the same painting as the transcript speaker. Unlike the
// conversation transcript (which groups by day and shows the clock only), grouped
// views aren't day-bounded, so the stamp carries the full `YYYY-MM-DD HH:MM`.
// name "" falls back to id; id "" renders "unknown".
func SpeakerLine(ts, name, id, note string, opts TranscriptOptions) string {
	loc := opts.Loc
	if loc == nil {
		loc = time.Local
	}
	_, clock, _, unix := formatTranscriptTime(ts, loc)
	stamp := clock
	if unix >= 0 {
		stamp = time.Unix(unix, 0).In(loc).Format("2006-01-02 15:04")
	}
	if id == "" {
		id = "unknown"
	}
	if name == "" {
		name = id
	}

	var b strings.Builder
	b.WriteString(paint(opts.Color, ansiDim, "["+stamp+"]"))
	b.WriteString(" <")
	b.WriteString(paint(opts.Color, ansiName, name))
	b.WriteString(paint(opts.Color, ansiDim, "|"+id))
	b.WriteString(">")
	if note != "" {
		b.WriteString(paint(opts.Color, ansiDim, " · "+note))
	}
	if opts.WithIDs && ts != "" {
		b.WriteString(paint(opts.Color, ansiDim, "  ⟨ts "+ts+"⟩"))
	}
	return b.String()
}
