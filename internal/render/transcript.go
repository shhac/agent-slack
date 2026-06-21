package render

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TranscriptMessage is one message to render in a transcript, paired with the
// thread depth it sits at (0 = a root/top-level message, 1 = a direct reply,
// etc.) so nested replies read as a thread.
type TranscriptMessage struct {
	Summary MessageSummary
	// Edited marks the message as having been edited (a `(edited)` header tag).
	Edited bool
	// BotName is the display name for a bot/app author (from the message's
	// username / bot_profile.name). When set and the message has no human User,
	// the speaker renders as `<BotName|app>`.
	BotName string
	// Depth is the thread-nesting level; replies render indented one level
	// under their parent.
	Depth int
}

// TranscriptOptions controls RenderTranscript. The resolver funcs map ids to
// human names; both may be nil (ids fall back to their bare form). Keeping
// these as plain funcs lets the render layer stay independent of the slack
// client that supplies the names.
type TranscriptOptions struct {
	// Loc is the zone message times are displayed in (time.Local when nil).
	Loc *time.Location
	// WithIDs appends each message's opaque ts id after the speaker token.
	WithIDs bool
	// SlackMarkdown keeps native Slack mrkdwn in bodies instead of converting
	// emphasis/links to prose.
	SlackMarkdown bool
	// UserName resolves a user id (U…/W…) to a display name; "" means unknown
	// (the bare id is shown). Used for the speaker, @mentions, and reactors.
	UserName func(id string) string
}

// transcriptIndent is one level of thread indentation.
const transcriptIndent = "  "

// markdownLinkRe matches the [label](url) form MrkdwnToMarkdown emits, so the
// transcript can rewrite it to the prose form `label (url)`.
var markdownLinkRe = regexp.MustCompile(`\[([^\]]*)\]\((https?://[^)]+)\)`)

// mentionAtIDRe matches the `@U123`/`@W123` residue MrkdwnToMarkdown leaves for
// bare mention tokens (those without an inline label), so we can swap in a
// resolved display name.
var mentionAtIDRe = regexp.MustCompile(`@([UW][A-Z0-9]{7,})\b`)

// RenderTranscript renders a chronological run of messages as natural-language
// text — one header line per message, the body indented under it, a blank line
// between messages, and thread replies indented a further level. It reuses the
// compact/mrkdwn machinery for bodies and only post-processes mentions and
// links into prose.
func RenderTranscript(messages []TranscriptMessage, opts TranscriptOptions) string {
	var b strings.Builder
	for i, m := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderTranscriptMessage(m, opts))
	}
	return b.String()
}

func renderTranscriptMessage(m TranscriptMessage, opts TranscriptOptions) string {
	base := strings.Repeat(transcriptIndent, max(m.Depth, 0))
	bodyIndent := base + transcriptIndent

	var b strings.Builder
	b.WriteString(base)
	b.WriteString(transcriptHeader(m, opts))
	b.WriteString("\n")

	for _, line := range transcriptBodyLines(m, opts) {
		b.WriteString(bodyIndent)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// transcriptHeader builds `[<date> @ <HH:MM:SS> (<tz>)] <Speaker|id>` plus the
// optional `(edited)` tag and `⟨ts …⟩` id region.
func transcriptHeader(m TranscriptMessage, opts TranscriptOptions) string {
	loc := opts.Loc
	if loc == nil {
		loc = time.Local
	}
	stamp, zone := formatTranscriptTime(m.Summary.TS, loc)
	speaker := transcriptSpeaker(m, opts)

	header := fmt.Sprintf("[%s (%s)] %s", stamp, zone, speaker)
	if m.Edited {
		header += " (edited)"
	}
	if opts.WithIDs && m.Summary.TS != "" {
		header += "  ⟨ts " + m.Summary.TS + "⟩"
	}
	return header
}

// formatTranscriptTime renders a slack ts (seconds.micros) as
// `YYYY-MM-DD @ HH:MM:SS` in loc, returning the friendly zone abbreviation
// separately. A ts that doesn't parse yields the raw ts as the stamp.
func formatTranscriptTime(ts string, loc *time.Location) (stamp, zone string) {
	secs := ts
	if dot := strings.IndexByte(ts, '.'); dot >= 0 {
		secs = ts[:dot]
	}
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return ts, loc.String()
	}
	t := time.Unix(n, 0).In(loc)
	name, _ := t.Zone()
	return t.Format("2006-01-02 @ 15:04:05"), name
}

// transcriptSpeaker renders the `<DisplayName|UserID>` token. Bot/app authors
// render `<BotName|app>`; unknown human users fall back to `<U…|U…>`.
func transcriptSpeaker(m TranscriptMessage, opts TranscriptOptions) string {
	if m.Summary.User == "" && m.BotName != "" {
		return "<" + m.BotName + "|app>"
	}
	id := m.Summary.User
	if id == "" {
		if m.Summary.BotID != "" {
			return "<" + m.Summary.BotID + "|app>"
		}
		return "<unknown|unknown>"
	}
	name := id
	if opts.UserName != nil {
		if resolved := opts.UserName(id); resolved != "" {
			name = resolved
		}
	}
	return "<" + name + "|" + id + ">"
}

// transcriptBodyLines builds the indented body: the prose-rendered content,
// then file lines, then a reaction trailer.
func transcriptBodyLines(m TranscriptMessage, opts TranscriptOptions) []string {
	var lines []string

	content := transcriptContent(m.Summary, opts)
	if content != "" {
		lines = append(lines, strings.Split(content, "\n")...)
	}

	for _, f := range m.Summary.Files {
		name := f.Name
		if name == "" {
			name = f.Title
		}
		if name == "" {
			name = f.ID
		}
		lines = append(lines, "[file: "+name+"]")
	}

	if trailer := transcriptReactions(m.Summary.Reactions, opts); trailer != "" {
		lines = append(lines, trailer)
	}
	return lines
}

// transcriptContent renders the message body to prose: it reuses
// renderContent (blocks/attachments/rich_text/emoji/emphasis) then rewrites
// markdown links to `label (url)` and bare @id mentions to @DisplayName.
func transcriptContent(msg MessageSummary, opts TranscriptOptions) string {
	content := renderContent(msg.Text, msg.Blocks, msg.Attachments, opts.SlackMarkdown)
	if content == "" {
		return ""
	}
	content = markdownLinkRe.ReplaceAllString(content, "$1 ($2)")
	if opts.UserName != nil {
		content = mentionAtIDRe.ReplaceAllStringFunc(content, func(token string) string {
			id := strings.TrimPrefix(token, "@")
			if name := opts.UserName(id); name != "" {
				return "@" + name
			}
			return token
		})
	}
	return content
}

// transcriptReactions builds `↳ 👍 Alice, Bob` from raw reaction objects, using
// the unicode emoji where known and resolving reactor ids to names.
func transcriptReactions(reactions []any, opts TranscriptOptions) string {
	compact := CompactReactions(reactions)
	if len(compact) == 0 {
		return ""
	}
	var parts []string
	for _, r := range compact {
		// EmojifyShortcodes leaves unknown :names: untouched, which is the
		// desired fallback.
		glyph := EmojifyShortcodes(":" + r.Name + ":")
		var names []string
		for _, id := range r.Users {
			name := id
			if opts.UserName != nil {
				if resolved := opts.UserName(id); resolved != "" {
					name = resolved
				}
			}
			names = append(names, name)
		}
		seg := glyph
		if len(names) > 0 {
			seg += " " + strings.Join(names, ", ")
		}
		parts = append(parts, seg)
	}
	return "↳ " + strings.Join(parts, "   ")
}
