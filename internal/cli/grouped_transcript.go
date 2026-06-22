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
