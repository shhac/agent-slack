package render

import (
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
	// ChannelName and UsergroupName resolve a channel id (C…/G…) to its name and
	// a usergroup id (S…) to its handle, for inline #channel / @group mentions in
	// the body; "" means unknown (the token is left as-is). Both may be nil.
	ChannelName   func(id string) string
	UsergroupName func(id string) string
	// Color emits ANSI styling (dim metadata, bold speaker names, dim tree
	// glyphs). The caller decides this from the output target (a TTY) and the
	// NO_COLOR/CLICOLOR_FORCE conventions; the render layer just honors it.
	Color bool
	// InlineEmoji, when set, turns a custom-emoji shortcode name (no colons)
	// into a terminal escape sequence that draws the emoji's image inline; ""
	// leaves the shortcode as text. It is the seam for the Kitty-graphics
	// inline-image mode — nil in every machine-output path, so the render layer
	// needs no knowledge of the graphics protocol or the Slack client behind
	// it. Applied last (after link/mention rewriting and truncation) so an
	// escape sequence is never split.
	InlineEmoji func(name string) string
	// Hyperlink, when set, renders a markdown link's label as an OSC 8 terminal
	// hyperlink to its url (encode(url, label)); nil falls back to the plain
	// "label (url)" form. Like InlineEmoji it is the seam for a terminal feature
	// the render layer stays ignorant of — nil on every non-TTY path.
	Hyperlink func(url, label string) string
}

// mentionResolvers bundles the per-entity resolvers for inline body rewriting.
func (o TranscriptOptions) mentionResolvers() MentionResolvers {
	return MentionResolvers{User: o.UserName, Channel: o.ChannelName, Usergroup: o.UsergroupName}
}

// transcriptIndent is one level of indentation for a root message's body.
const transcriptIndent = "  "

// transcriptGroupWindowSecs is how close in time (and same author, same depth,
// same day) two consecutive messages must be for the second to render under a
// collapsed header — the run reads as one person speaking, like Slack's UI.
const transcriptGroupWindowSecs = 300

// ANSI SGR sequences used when TranscriptOptions.Color is set. A full reset
// (0m) closes every span — simpler and safer than tracking per-attribute
// resets, and transcript spans never nest.
const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
	ansiName  = "\x1b[1;36m" // bold cyan, for speaker display names
)

// paint wraps s in an SGR code when on (and s is non-empty), else returns s.
func paint(on bool, code, s string) string {
	if !on || s == "" {
		return s
	}
	return code + s + ansiReset
}

// mentionAtIDRe matches the `@U123`/`@W123` residue MrkdwnToMarkdown leaves for
// bare mention tokens (those without an inline label), so we can swap in a
// resolved display name. No trailing \b: the id char class ([A-Z0-9]) already
// ends the match at the first lowercase/space/punctuation, and a real boundary
// assertion would *miss* a mention butted straight against following prose —
// Slack allows `<@U…>for`, which renders as `@U…for` with no space.
var mentionAtIDRe = regexp.MustCompile(`@([UW][A-Z0-9]{8,})`)

// transcriptMeta is the per-message data RenderTranscript precomputes so the
// render pass can see neighbours (grouping, day rollovers, last-reply) without
// re-parsing timestamps.
type transcriptMeta struct {
	date    string // YYYY-MM-DD in the display zone ("" if ts unparseable)
	clock   string // HH:MM:SS, or the raw ts when unparseable
	zone    string // friendly zone abbreviation
	tsUnix  int64  // -1 when unparseable
	speaker string // grouping key (user id / bot name); "" never groups
	depth   int
}

// RenderTranscript renders a chronological run of messages as natural-language
// text. A `──── <date> (<zone>) ────` divider opens each new day; headers carry
// the time only. Consecutive messages from the same author within a short
// window collapse under one header (no repeated speaker, no blank gap). Thread
// replies render as a `├─`/`└─` tree under their root. It reuses the
// compact/mrkdwn machinery for bodies and only post-processes mentions/links.
func RenderTranscript(messages []TranscriptMessage, opts TranscriptOptions) string {
	loc := opts.Loc
	if loc == nil {
		loc = time.Local
	}

	metas := make([]transcriptMeta, len(messages))
	for i, m := range messages {
		metas[i] = newTranscriptMeta(m, loc)
	}

	var b strings.Builder
	prevDate := ""
	for i, m := range messages {
		meta := metas[i]
		newDay := meta.date != "" && meta.date != prevDate
		grouped := !newDay && i > 0 && groupedWith(metas[i-1], meta)

		switch {
		case newDay:
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(daySeparator(meta.date, meta.zone, opts))
			b.WriteString("\n")
			prevDate = meta.date
		case i > 0 && !grouped:
			b.WriteString("\n")
		}

		lastReply := i == len(messages)-1 || metas[i+1].depth < meta.depth
		b.WriteString(renderTranscriptMessage(m, meta, opts, grouped, lastReply))
	}
	return b.String()
}

// groupedWith reports whether cur should render under prev's collapsed header:
// same author, same depth, same day, and close in time.
func groupedWith(prev, cur transcriptMeta) bool {
	return cur.speaker != "" && cur.speaker == prev.speaker &&
		cur.depth == prev.depth &&
		cur.date != "" && cur.date == prev.date &&
		cur.tsUnix >= 0 && prev.tsUnix >= 0 &&
		cur.tsUnix-prev.tsUnix <= transcriptGroupWindowSecs
}

func newTranscriptMeta(m TranscriptMessage, loc *time.Location) transcriptMeta {
	date, clock, zone, unix := formatTranscriptTime(m.Summary.TS, loc)
	return transcriptMeta{
		date:    date,
		clock:   clock,
		zone:    zone,
		tsUnix:  unix,
		speaker: speakerKey(m),
		depth:   max(m.Depth, 0),
	}
}

// daySeparator renders the `──── 2026-06-21 (UTC) ────` divider opening a day.
func daySeparator(date, zone string, opts TranscriptOptions) string {
	label := date
	if zone != "" {
		label += " (" + zone + ")"
	}
	return paint(opts.Color, ansiDim, "──── "+label+" ────")
}

func renderTranscriptMessage(m TranscriptMessage, meta transcriptMeta, opts TranscriptOptions, grouped, lastReply bool) string {
	headerPrefix, bodyPrefix := transcriptPrefixes(meta.depth, lastReply, opts)

	var b strings.Builder
	b.WriteString(headerPrefix)
	b.WriteString(transcriptHeader(m, meta, opts, grouped))
	b.WriteString("\n")

	for _, line := range transcriptBodyLines(m, opts) {
		b.WriteString(bodyPrefix)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// transcriptPrefixes builds the leading glyphs for a message's header and body
// lines. Root messages (depth 0) have no header prefix and a two-space body
// indent; replies get a `├─`/`└─` connector and a `│ `/space continuation that
// keeps the body aligned under the header.
func transcriptPrefixes(depth int, lastReply bool, opts TranscriptOptions) (header, body string) {
	if depth <= 0 {
		return "", transcriptIndent
	}
	indent := strings.Repeat("   ", depth-1)
	connector, cont := "├─ ", "│  "
	if lastReply {
		connector, cont = "└─ ", "   "
	}
	return indent + paint(opts.Color, ansiDim, connector), indent + paint(opts.Color, ansiDim, cont)
}

// transcriptHeader builds `[<HH:MM:SS>] <Speaker|id>` plus the optional
// `(edited)` tag and `⟨ts …⟩` id region. The speaker is dropped when grouped
// (the message continues a run from the same author).
func transcriptHeader(m TranscriptMessage, meta transcriptMeta, opts TranscriptOptions, grouped bool) string {
	var b strings.Builder
	b.WriteString(paint(opts.Color, ansiDim, "["+meta.clock+"]"))
	if !grouped {
		b.WriteString(" ")
		b.WriteString(transcriptSpeaker(m, opts))
	}
	if m.Summary.Edited {
		b.WriteString(paint(opts.Color, ansiDim, " (edited)"))
	}
	if opts.WithIDs && m.Summary.TS != "" {
		b.WriteString(paint(opts.Color, ansiDim, "  ⟨ts "+m.Summary.TS+"⟩"))
	}
	return b.String()
}

// formatTranscriptTime renders a slack ts (seconds.micros) in loc, returning
// the date (YYYY-MM-DD), the clock (HH:MM:SS), the friendly zone abbreviation,
// and the unix seconds. A ts that doesn't parse yields an empty date, the raw
// ts as the clock, and -1 seconds (so it neither groups nor opens a day).
func formatTranscriptTime(ts string, loc *time.Location) (date, clock, zone string, unix int64) {
	secs := ts
	if dot := strings.IndexByte(ts, '.'); dot >= 0 {
		secs = ts[:dot]
	}
	n, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return "", ts, loc.String(), -1
	}
	t := time.Unix(n, 0).In(loc)
	name, _ := t.Zone()
	return t.Format("2006-01-02"), t.Format("15:04:05"), name, n
}

// resolveName resolves id to a display name using opts.UserName, falling back
// to id itself when the resolver is nil or returns "".
func resolveName(opts TranscriptOptions, id string) string {
	if opts.UserName != nil {
		if resolved := opts.UserName(id); resolved != "" {
			return resolved
		}
	}
	return id
}

// transcriptSpeaker renders the `<DisplayName|UserID>` token. Bot/app authors
// render `<BotName|app>`; unknown human users fall back to `<U…|U…>`. When
// Color is set the display name is bold cyan and the `|id` region is dimmed.
func transcriptSpeaker(m TranscriptMessage, opts TranscriptOptions) string {
	name, id := speakerParts(m, opts)
	return "<" + paint(opts.Color, ansiName, name) + paint(opts.Color, ansiDim, "|"+id) + ">"
}

// speakerParts resolves a message to its (display name, id-suffix) pair: app
// authors carry the literal "app" suffix, unknown authors render "unknown".
func speakerParts(m TranscriptMessage, opts TranscriptOptions) (name, id string) {
	botName := m.Summary.BotName
	if m.Summary.User == "" && botName != "" {
		return botName, "app"
	}
	uid := m.Summary.User
	if uid == "" {
		if m.Summary.BotID != "" {
			return m.Summary.BotID, "app"
		}
		return "unknown", "unknown"
	}
	return resolveName(opts, uid), uid
}

// speakerKey is the stable identity used to group consecutive messages; it is
// independent of name resolution and color. "" for an unknown author so such
// messages never collapse into a run.
func speakerKey(m TranscriptMessage) string {
	if m.Summary.User != "" {
		return "u:" + m.Summary.User
	}
	if botName := m.Summary.BotName; botName != "" {
		return "b:" + botName
	}
	if m.Summary.BotID != "" {
		return "b:" + m.Summary.BotID
	}
	return ""
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
	return FinalizeContent(content, opts.mentionResolvers(), opts)
}

// transcriptReactions builds `↳ 👍 Alice, Bob` from raw reaction objects, using
// the unicode emoji where known and resolving reactor ids to names. The whole
// trailer is dimmed when Color is set.
func transcriptReactions(reactions []any, opts TranscriptOptions) string {
	compact := CompactReactions(reactions)
	if len(compact) == 0 {
		return ""
	}
	var parts []string
	for _, r := range compact {
		// EmojifyShortcodes leaves unknown :names: untouched, which is the
		// desired fallback; applyInlineEmoji then swaps a custom emoji for its
		// inline image when that mode is on (a no-op otherwise, and on the
		// unicode glyph a standard emoji already became).
		glyph := applyInlineEmoji(EmojifyShortcodes(":"+r.Name+":"), opts.InlineEmoji)
		var names []string
		for _, id := range r.Users {
			names = append(names, resolveName(opts, id))
		}
		seg := glyph
		if len(names) > 0 {
			seg += " " + strings.Join(names, ", ")
		}
		parts = append(parts, seg)
	}
	return paint(opts.Color, ansiDim, "↳ "+strings.Join(parts, "   "))
}
