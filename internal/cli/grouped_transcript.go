package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	output "github.com/shhac/lib-agent-output"

	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// This file renders --format transcript for the grouped/list commands (unreads,
// later, drafts) on top of render.RenderGrouped — the digest sibling of the
// conversation transcript that backs message get/list. Each builder maps the
// command's compact structs into a render.Grouped; the JSON paths are untouched.

func transcriptOpts(globals *GlobalFlags, tflags *transcriptFlags) (render.TranscriptOptions, error) {
	loc, err := tflags.location()
	if err != nil {
		return render.TranscriptOptions{}, err
	}
	return render.TranscriptOptions{
		Loc:     loc,
		WithIDs: tflags.withIDs,
		Color:   output.Enabled(globals.stdout),
	}, nil
}

func writeGrouped(globals *GlobalFlags, g render.Grouped, opts render.TranscriptOptions) error {
	_, err := globals.stdout.Write([]byte(render.RenderGrouped(g, opts)))
	return err
}

// groupedNameResolver resolves a fixed set of user ids to display names up front
// (cache-then-fetch), returning "" for unknowns so SpeakerLine falls back to the
// bare id — mirroring transcriptUserResolver for the non-MessageSummary structs.
func groupedNameResolver(ctx context.Context, cc *clientContext, ids []string) func(string) string {
	users, _ := slack.ResolveUsersByID(ctx, cc.Client, ids, slack.ResolveCacheThenFetch)
	return func(id string) string {
		u, ok := users[id]
		if !ok {
			return ""
		}
		switch {
		case u.DisplayName != "":
			return u.DisplayName
		case u.RealName != "":
			return u.RealName
		default:
			return u.Name
		}
	}
}

// authorIDName turns a compact author into the (id, display-name) pair
// SpeakerLine wants: a human user resolves via the resolver; a bot keeps its id
// (name falls back to the id); a nil author renders as unknown.
func authorIDName(a *render.CompactAuthor, resolve func(string) string) (id, name string) {
	if a == nil {
		return "", ""
	}
	if a.UserID != "" {
		return a.UserID, resolve(a.UserID)
	}
	return a.BotID, ""
}

// bodyLines splits pre-rendered content into transcript body lines; empty
// content yields no lines (the header stands alone).
func bodyLines(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

// countLabel renders "1 draft" / "3 drafts".
func countLabel(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func replyNote(n int) string {
	if n <= 0 {
		return ""
	}
	return countLabel(n, "reply", "replies")
}

func groupedStamp(unix int64, loc *time.Location) string {
	return time.Unix(unix, 0).In(loc).Format("2006-01-02 15:04")
}

// --- canvas (document) -------------------------------------------------------

// writeCanvasTranscript prints a canvas as the document it is: its Markdown body
// verbatim under an optional dim `──── <title> ────` divider — the third
// transcript render family, neither conversation nor entity digest.
func writeCanvasTranscript(globals *GlobalFlags, c slack.Canvas) error {
	color := output.Enabled(globals.stdout)
	var b strings.Builder
	if c.Title != "" {
		b.WriteString(render.Dim("──── "+c.Title+" ────", color))
		b.WriteString("\n\n")
	}
	b.WriteString(c.Markdown)
	if !strings.HasSuffix(c.Markdown, "\n") {
		b.WriteString("\n")
	}
	_, err := globals.stdout.Write([]byte(b.String()))
	return err
}

// --- entity digests (channels / users / usergroups) --------------------------

// digestSummary builds the `<Noun> · N items[ · more available]` divider label.
func digestSummary(noun string, label string, hasMore bool) string {
	summary := noun + " · " + label
	if hasMore {
		summary += " · more available"
	}
	return summary
}

// digestSections wraps the item blocks plus, when any lookup missed, a trailing
// "Unresolved" section so a get over several targets reports gaps honestly.
func digestSections(items []render.GroupItem, unresolved []string, color bool) []render.GroupSection {
	sections := []render.GroupSection{{Items: items}}
	if len(unresolved) > 0 {
		missed := make([]render.GroupItem, 0, len(unresolved))
		for _, label := range unresolved {
			missed = append(missed, render.GroupItem{Title: render.Dim("⚠ "+label+" — not found", color)})
		}
		sections = append(sections, render.GroupSection{Heading: "Unresolved", Items: missed})
	}
	return sections
}

// collectEntityGet resolves each target, partitioning successes from the args
// that failed to resolve (rendered under "Unresolved").
func collectEntityGet[T any](args []string, get func(string) (T, error)) (items []T, unresolved []string) {
	for _, a := range args {
		it, err := get(a)
		if err != nil {
			unresolved = append(unresolved, a)
			continue
		}
		items = append(items, it)
	}
	return items, unresolved
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func renderChannelsDigest(ctx context.Context, globals *GlobalFlags, cc *clientContext, tflags *transcriptFlags, channels []slack.CompactChannel, unresolved []string, hasMore bool) error {
	opts, err := transcriptOpts(globals, tflags)
	if err != nil {
		return err
	}
	var counterpartIDs []string
	for _, ch := range channels {
		if ch.IsIM && ch.User != "" {
			counterpartIDs = append(counterpartIDs, ch.User)
		}
	}
	resolve := groupedNameResolver(ctx, cc, counterpartIDs)

	items := make([]render.GroupItem, 0, len(channels))
	for _, ch := range channels {
		items = append(items, channelDigestItem(ch, resolve, opts.Color))
	}
	g := render.Grouped{
		Summary:  digestSummary("Channels", countLabel(len(channels), "channel", "channels"), hasMore),
		Sections: digestSections(items, unresolved, opts.Color),
		Empty:    "No channels.",
	}
	return writeGrouped(globals, g, opts)
}

func channelDigestItem(ch slack.CompactChannel, resolve func(string) string, color bool) render.GroupItem {
	label := "#" + firstNonEmpty(ch.Name, ch.ID)
	switch {
	case ch.IsIM:
		name := resolve(ch.User)
		label = "@" + firstNonEmpty(name, ch.User, ch.ID) + " (DM)"
	case ch.IsMpim:
		label = firstNonEmpty(ch.Name, ch.ID) + " (group DM)"
	}

	var badges []string
	if ch.NumMembers > 0 {
		badges = append(badges, countLabel(ch.NumMembers, "member", "members"))
	}
	if ch.IsPrivate {
		badges = append(badges, "🔒 private")
	}
	if ch.IsArchived {
		badges = append(badges, "🗄 archived")
	}
	if ch.IsMember {
		badges = append(badges, "✓ member")
	}

	title := render.Emphasize(label, color)
	if len(badges) > 0 {
		title += render.Dim(" · "+strings.Join(badges, " · "), color)
	}
	var details []string
	if ch.Topic != "" {
		details = []string{render.Dim(ch.Topic, color)}
	}
	return render.GroupItem{Title: title, Details: details}
}

func renderUsersDigest(globals *GlobalFlags, tflags *transcriptFlags, users []slack.CompactUser, unresolved []string, hasMore bool) error {
	opts, err := transcriptOpts(globals, tflags)
	if err != nil {
		return err
	}
	items := make([]render.GroupItem, 0, len(users))
	for _, u := range users {
		items = append(items, userDigestItem(u, opts.Color))
	}
	g := render.Grouped{
		Summary:  digestSummary("Users", countLabel(len(users), "user", "users"), hasMore),
		Sections: digestSections(items, unresolved, opts.Color),
		Empty:    "No users.",
	}
	return writeGrouped(globals, g, opts)
}

func userDigestItem(u slack.CompactUser, color bool) render.GroupItem {
	handle := "@" + firstNonEmpty(u.Name, u.ID)
	var parts []string
	if name := firstNonEmpty(u.DisplayName, u.RealName); name != "" {
		parts = append(parts, name)
	}
	if u.Title != "" {
		parts = append(parts, u.Title)
	}
	if u.TZ != "" {
		parts = append(parts, u.TZ)
	}
	if u.IsBot {
		parts = append(parts, "🤖 bot")
	}
	if u.Deleted {
		parts = append(parts, "deactivated")
	}
	if u.DMID != "" {
		parts = append(parts, "DM open")
	}
	title := render.Emphasize(handle, color)
	if len(parts) > 0 {
		title += render.Dim(" · "+strings.Join(parts, " · "), color)
	}
	return render.GroupItem{Title: title}
}

func renderUsergroupsDigest(globals *GlobalFlags, tflags *transcriptFlags, groups []slack.CompactUsergroup, unresolved []string, hasMore bool) error {
	opts, err := transcriptOpts(globals, tflags)
	if err != nil {
		return err
	}
	items := make([]render.GroupItem, 0, len(groups))
	for _, g := range groups {
		items = append(items, usergroupDigestItem(g, opts.Color))
	}
	grouped := render.Grouped{
		Summary:  digestSummary("Usergroups", countLabel(len(groups), "usergroup", "usergroups"), hasMore),
		Sections: digestSections(items, unresolved, opts.Color),
		Empty:    "No usergroups.",
	}
	return writeGrouped(globals, grouped, opts)
}

func usergroupDigestItem(g slack.CompactUsergroup, color bool) render.GroupItem {
	label := "@" + firstNonEmpty(g.Handle, g.ID)
	if g.Name != "" {
		label += " (" + g.Name + ")"
	}
	var badges []string
	if g.UserCount > 0 {
		badges = append(badges, countLabel(g.UserCount, "member", "members"))
	}
	if n := len(g.Channels); n > 0 {
		badges = append(badges, countLabel(n, "default channel", "default channels"))
	}
	if g.IsExternal {
		badges = append(badges, "external")
	}
	if g.Disabled {
		badges = append(badges, "disabled")
	}
	title := render.Emphasize(label, color)
	if len(badges) > 0 {
		title += render.Dim(" · "+strings.Join(badges, " · "), color)
	}
	var details []string
	if g.Description != "" {
		details = []string{render.Dim(g.Description, color)}
	}
	return render.GroupItem{Title: title, Details: details}
}

// writeMessageGetFooter appends a dim `└ thread: N replies · <permalink>` line
// after a single-message transcript, surfacing the thread summary and permalink
// the JSON payload carries but the conversation render otherwise drops.
func writeMessageGetFooter(ctx context.Context, globals *GlobalFlags, cc *clientContext, ref *render.MessageRef, msg render.MessageSummary) error {
	color := output.Enabled(globals.stdout)
	permalink := render.BuildMessageURL(render.MessageURLParts{
		WorkspaceURL: ref.WorkspaceURL,
		ChannelID:    ref.ChannelID,
		MessageTS:    msg.TS,
		ThreadTS:     msg.ThreadTS,
	})
	footer := ""
	if thread, err := slack.ThreadSummary(ctx, cc.Client, ref.ChannelID, msg); err == nil && thread != nil {
		if replies := thread.Length - 1; replies > 0 {
			footer = fmt.Sprintf("thread: %s · ", countLabel(replies, "reply", "replies"))
		}
	}
	footer += permalink
	_, err := fmt.Fprintln(globals.stdout, render.Dim("└ "+footer, color))
	return err
}

// --- unreads -----------------------------------------------------------------

func renderUnreadsTranscript(ctx context.Context, globals *GlobalFlags, cc *clientContext, tflags *transcriptFlags, channels []slack.UnreadChannel) error {
	opts, err := transcriptOpts(globals, tflags)
	if err != nil {
		return err
	}
	resolve := groupedNameResolver(ctx, cc, unreadAuthorIDs(channels))

	totalUnread, totalMentions := 0, 0
	sections := make([]render.GroupSection, 0, len(channels))
	for _, ch := range channels {
		totalUnread += ch.UnreadCount
		totalMentions += ch.MentionCount
		items := make([]render.GroupItem, 0, len(ch.Messages))
		for _, m := range ch.Messages {
			id, name := authorIDName(m.Author, resolve)
			items = append(items, render.GroupItem{
				Title:   render.SpeakerLine(m.TS, name, id, replyNote(m.ReplyCount), opts),
				Details: bodyLines(m.Content),
			})
		}
		sections = append(sections, render.GroupSection{Heading: unreadChannelLabel(ch), Items: items})
	}

	summary := fmt.Sprintf("Unreads · %s · %d unread", countLabel(len(channels), "channel", "channels"), totalUnread)
	if totalMentions > 0 {
		summary += fmt.Sprintf(" · %s", countLabel(totalMentions, "mention", "mentions"))
	}
	return writeGrouped(globals, render.Grouped{Summary: summary, Sections: sections, Empty: "No unread messages."}, opts)
}

func unreadAuthorIDs(channels []slack.UnreadChannel) []string {
	seen := map[string]bool{}
	var ids []string
	for _, ch := range channels {
		for _, m := range ch.Messages {
			if m.Author != nil && m.Author.UserID != "" && !seen[m.Author.UserID] {
				seen[m.Author.UserID] = true
				ids = append(ids, m.Author.UserID)
			}
		}
	}
	return ids
}

func unreadChannelLabel(c slack.UnreadChannel) string {
	label := c.ChannelID
	switch c.ChannelType {
	case "dm":
		if c.ChannelName != "" {
			label = "@" + c.ChannelName + " (DM)"
		}
	case "mpim":
		if c.ChannelName != "" {
			label = c.ChannelName + " (group DM)"
		}
	default:
		if c.ChannelName != "" {
			label = "#" + c.ChannelName
		}
	}
	label += fmt.Sprintf(" · %d unread", c.UnreadCount)
	if c.MentionCount > 0 {
		label += fmt.Sprintf(", %s", countLabel(c.MentionCount, "mention", "mentions"))
	}
	return label
}

// --- later -------------------------------------------------------------------

func renderLaterTranscript(ctx context.Context, globals *GlobalFlags, cc *clientContext, tflags *transcriptFlags, items []slack.LaterItem) error {
	opts, err := transcriptOpts(globals, tflags)
	if err != nil {
		return err
	}
	resolve := groupedNameResolver(ctx, cc, laterAuthorIDs(items))

	byState := map[string][]render.GroupItem{}
	counts := map[string]int{}
	for _, it := range items {
		counts[it.State]++
		item := render.GroupItem{Lead: laterLead(ctx, cc, it, opts.Loc)}
		if it.Message != nil {
			id, name := authorIDName(it.Message.Author, resolve)
			item.Title = render.SpeakerLine(it.TS, name, id, replyNote(it.Message.ReplyCount), opts)
			item.Details = bodyLines(it.Message.Content)
		} else {
			item.Title = render.SpeakerLine(it.TS, "", "", "", opts)
		}
		byState[it.State] = append(byState[it.State], item)
	}

	order := []struct{ key, heading string }{
		{"in_progress", "In progress"},
		{"completed", "Completed"},
		{"archived", "Archived"},
	}
	var sections []render.GroupSection
	var parts []string
	for _, o := range order {
		if n := counts[o.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ToLower(o.heading)))
		}
		if len(byState[o.key]) > 0 {
			sections = append(sections, render.GroupSection{Heading: o.heading, Items: byState[o.key]})
		}
	}
	summary := fmt.Sprintf("Later · %s", countLabel(len(items), "saved", "saved"))
	if len(parts) > 0 {
		summary += " · " + strings.Join(parts, " · ")
	}
	return writeGrouped(globals, render.Grouped{Summary: summary, Sections: sections, Empty: "No saved-for-later items."}, opts)
}

func laterAuthorIDs(items []slack.LaterItem) []string {
	seen := map[string]bool{}
	var ids []string
	for _, it := range items {
		if it.Message != nil && it.Message.Author != nil && it.Message.Author.UserID != "" && !seen[it.Message.Author.UserID] {
			seen[it.Message.Author.UserID] = true
			ids = append(ids, it.Message.Author.UserID)
		}
	}
	return ids
}

func laterLead(ctx context.Context, cc *clientContext, it slack.LaterItem, loc *time.Location) string {
	name := it.ChannelName
	if name == "" {
		name = slack.ResolveChannelName(ctx, cc.Client, it.ChannelID)
	}
	lead := fmt.Sprintf("#%s · saved %s", name, groupedStamp(it.DateSaved, loc))
	if it.DateCompleted > 0 {
		lead += " · done " + groupedStamp(it.DateCompleted, loc)
	}
	return lead
}

// --- drafts ------------------------------------------------------------------

func renderDraftsTranscript(ctx context.Context, globals *GlobalFlags, cc *clientContext, tflags *transcriptFlags, drafts []slack.Draft) error {
	opts, err := transcriptOpts(globals, tflags)
	if err != nil {
		return err
	}
	items := make([]render.GroupItem, 0, len(drafts))
	for _, d := range drafts {
		target := slack.ResolveChannelName(ctx, cc.Client, d.ChannelID)
		title := render.Emphasize(d.ID, opts.Color) + " → #" + target
		if d.PostAt > 0 {
			title += render.Dim(" · scheduled "+groupedStamp(d.PostAt, opts.Loc), opts.Color)
		}
		details := bodyLines(d.Text)
		if len(details) == 0 {
			details = []string{render.Dim("(no text)", opts.Color)}
		}
		if len(d.FileIDs) > 0 {
			details = append(details, "📎 "+countLabel(len(d.FileIDs), "file", "files"))
		}
		items = append(items, render.GroupItem{Title: title, Details: details})
	}
	g := render.Grouped{
		Summary:  "Drafts · " + countLabel(len(drafts), "draft", "drafts"),
		Sections: []render.GroupSection{{Items: items}},
		Empty:    "No drafts.",
	}
	return writeGrouped(globals, g, opts)
}
