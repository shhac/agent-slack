// Markdown lists → rich_text_list blocks. Slack has no nested-list container:
// one source list becomes several sibling blocks, nesting depth rides on
// `indent`, and a numbered run interrupted by a sub-list resumes via `offset`.
// Tracking both across block boundaries is what this file exists for.
package render

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	bulletLineRe  = regexp.MustCompile(`^(\s*)[•◦▪▫▸‣●○◆◇\-*]\s+(.*)$`)
	orderedLineRe = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
)

// tabStop is the column width a tab advances to when measuring list indent, so
// tab- and space-indented sources nest identically.
const tabStop = 4

// maxListIndent bounds the nesting depth. Slack documents no ceiling and stores
// what it is given, but real messages never nest this far; the cap keeps
// pathological input from putting an unbounded integer on the wire.
const maxListIndent = 8

// listLine is one parsed source list item.
type listLine struct {
	width int    // leading indent in columns, tabs expanded
	style string // "bullet" | "ordered"
	start int    // the literal number written on an ordered line
	text  string
}

// parseListLine classifies a line as a list item. Bullets are tried first so a
// "- " marker wins over any digits that follow it.
func parseListLine(line string) (listLine, bool) {
	if m := bulletLineRe.FindStringSubmatch(line); m != nil {
		return listLine{width: indentWidth(m[1]), style: "bullet", start: 1, text: m[2]}, true
	}
	if m := orderedLineRe.FindStringSubmatch(line); m != nil {
		start, err := strconv.Atoi(m[2])
		if err != nil {
			start = 1 // a number too long for an int; treat it as an ordinary list
		}
		return listLine{width: indentWidth(m[1]), style: "ordered", start: start, text: m[3]}, true
	}
	return listLine{}, false
}

func isListLine(line string) bool {
	_, ok := parseListLine(line)
	return ok
}

// indentWidth measures leading whitespace in columns.
func indentWidth(s string) int {
	width := 0
	for _, r := range s {
		if r == '\t' {
			width += tabStop - width%tabStop
			continue
		}
		width++
	}
	return width
}

// resumesList reports whether a list continues after the blank lines at idx. A
// blank line between items makes one loose list, not two lists, so numbering
// has to survive it.
func resumesList(lines []string, idx int) bool {
	for idx < len(lines) && strings.TrimSpace(lines[idx]) == "" {
		idx++
	}
	return idx < len(lines) && isListLine(lines[idx])
}

// listRun is a maximal stretch of items sharing one depth and style — the unit
// that becomes a single rich_text_list element.
type listRun struct {
	depth int
	style string
	start int // first item's literal number, seeding a fresh ordered list
	items []any
}

// listLevel is one open nesting level: the indent width that opened it, and the
// number an ordered run at this level resumes from. Keeping the width and the
// numbering together means closing a level discards its count for free — the
// two were once separate structures that had to be pruned in lockstep by hand.
type listLevel struct {
	width   int
	next    int  // offset the next ordered run at this level resumes from
	counted bool // an ordered run has already closed here, so next is live
}

// depthOf resolves an indent width to a nesting depth against the levels
// currently open, without mutating them — the caller opens or closes levels
// once it has flushed any run that belonged to the old depth. Widths are only
// ever compared to the ones already opened, so 2-space, 4-space and tab
// conventions all nest, and an outdent to a width nobody opened lands on the
// nearest enclosing level rather than inventing one.
func depthOf(levels []listLevel, width int) int {
	if len(levels) == 0 || width > levels[len(levels)-1].width {
		// Capping here rather than at emit time keeps depth and indent the same
		// number, so everything past the cap collapses into one run instead of
		// becoming sibling runs that share an indent and read as un-nested.
		return min(len(levels), maxListIndent)
	}
	depth := len(levels) - 1
	for depth > 0 && width < levels[depth].width {
		depth--
	}
	return depth
}

// collectList consumes a run of consecutive list lines — bullets and numbers
// mixed, at any nesting depth — and appends one rich_text_list element per
// (depth, style) run. Slack has no nested-list container: depth rides on
// `indent`, and a numbered run interrupted by a sub-list resumes via `offset`.
// Both therefore have to be tracked ACROSS run boundaries, which is why one
// call handles both styles instead of one call per contiguous same-style run.
func collectList(lines []string, startIdx int, inline func(string) []InlineElement, elements *[]RichTextElement) int {
	var levels []listLevel // open nesting levels, outermost first
	var run *listRun

	flush := func() {
		if run == nil {
			return
		}
		el := RichTextElement{
			Type:     "rich_text_list",
			Style:    run.style,
			Indent:   run.depth,
			Elements: run.items,
		}
		if run.style == "ordered" {
			level := &levels[run.depth]
			if !level.counted {
				level.next = run.start - 1
				level.counted = true
			}
			el.Offset = level.next
			level.next += len(run.items)
		}
		*elements = append(*elements, el)
		run = nil
	}

	idx := startIdx
	for idx < len(lines) {
		item, ok := parseListLine(lines[idx])
		if !ok {
			if strings.TrimSpace(lines[idx]) == "" && resumesList(lines, idx+1) {
				idx++
				continue
			}
			break
		}

		// Flush before touching levels: the outgoing run still needs its own
		// level to record where its numbering finished.
		depth := depthOf(levels, item.width)
		if run != nil && (run.depth != depth || run.style != item.style) {
			flush()
		}
		if depth == len(levels) {
			levels = append(levels, listLevel{width: item.width})
		} else {
			// Outdenting closes every deeper level. Their counts go with them, so
			// the next sub-list starts at 1 rather than resuming a closed sibling.
			levels = levels[:depth+1]
		}

		if run == nil {
			run = &listRun{depth: depth, style: item.style, start: item.start}
		}
		run.items = append(run.items, RichTextElement{
			Type:     "rich_text_section",
			Elements: inlineToAny(inline(item.text)),
		})
		idx++
	}

	flush()
	return idx
}
