package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// Conversation-list transcripts: unreads (grouped by channel), later (grouped by
// state), and drafts (a flat pending-message listing). Each maps its command's
// compact structs onto the shared grouped core.

// --- unreads -----------------------------------------------------------------

func renderUnreadsTranscript(ctx context.Context, globals *GlobalFlags, cc *clientContext, tflags *transcriptFlags, channels []slack.UnreadChannel) error {
	opts, err := transcriptOpts(globals, tflags)
	if err != nil {
		return err
	}
	mode, err := parseResolveMode(tflags.resolve)
	if err != nil {
		return err
	}
	var authors, contents []string
	for _, ch := range channels {
		for _, m := range ch.Messages {
			authors, contents = appendAuthorContent(authors, contents, m.Author, m.Content)
		}
	}
	resolvers := digestResolvers(ctx, globals, cc, mode, authors, contents)

	totalUnread, totalMentions := 0, 0
	sections := make([]render.GroupSection, 0, len(channels))
	for _, ch := range channels {
		totalUnread += ch.UnreadCount
		totalMentions += ch.MentionCount
		items := make([]render.GroupItem, 0, len(ch.Messages))
		for _, m := range ch.Messages {
			id, name := authorIDName(m.Author, resolvers.User)
			items = append(items, render.GroupItem{
				Title:   render.SpeakerLine(m.TS, name, id, replyNote(m.ReplyCount), opts),
				Details: bodyLines(render.ResolveMentionsForDisplay(m.Content, resolvers)),
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

// digestResolvers builds the inline mention resolvers for a digest transcript:
// every entity referenced in the rendered message bodies, plus the speaker
// (author) ids, resolved under the --resolve mode. It is the digest counterpart
// of printTranscript's resolver wiring — same ResolveReferenced machinery, but
// sourcing ids from the already-rendered content (CollectDisplayIDs) since the
// digest kept no raw blocks.
func digestResolvers(ctx context.Context, globals *GlobalFlags, cc *clientContext, mode resolveMode, authorIDs, contents []string) render.MentionResolvers {
	refs := render.CollectDisplayIDs(contents...)
	refs.Users = append(refs.Users, authorIDs...)
	return transcriptResolvers(ctx, globals, cc, refs, mode)
}

// appendAuthorContent accumulates an author's user id (for speaker resolution)
// and the message content (for body-mention collection) in one step.
func appendAuthorContent(authors, contents []string, author *render.CompactAuthor, content string) ([]string, []string) {
	if author != nil && author.UserID != "" {
		authors = append(authors, author.UserID)
	}
	return authors, append(contents, content)
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
	mode, err := parseResolveMode(tflags.resolve)
	if err != nil {
		return err
	}
	var authors, contents []string
	for _, it := range items {
		if it.Message != nil {
			authors, contents = appendAuthorContent(authors, contents, it.Message.Author, it.Message.Content)
		}
	}
	resolvers := digestResolvers(ctx, globals, cc, mode, authors, contents)

	byState := map[string][]render.GroupItem{}
	counts := map[string]int{}
	for _, it := range items {
		counts[it.State]++
		byState[it.State] = append(byState[it.State], laterDigestItem(ctx, cc, it, resolvers, opts))
	}

	sections, summarySuffix := laterSections(byState, counts)
	summary := fmt.Sprintf("Later · %s", countLabel(len(items), "saved", "saved")) + summarySuffix
	return writeGrouped(globals, render.Grouped{Summary: summary, Sections: sections, Empty: "No saved-for-later items."}, opts)
}

// laterDigestItem builds one saved-item block: the channel/saved-time lead, then
// the saved message as a speaker line (or a bare timestamp when the message body
// wasn't fetched).
func laterDigestItem(ctx context.Context, cc *clientContext, it slack.LaterItem, resolvers render.MentionResolvers, opts render.TranscriptOptions) render.GroupItem {
	item := render.GroupItem{Lead: laterLead(ctx, cc, it, opts.Loc)}
	if it.Message == nil {
		item.Title = render.SpeakerLine(it.TS, "", "", "", opts)
		return item
	}
	id, name := authorIDName(it.Message.Author, resolvers.User)
	item.Title = render.SpeakerLine(it.TS, name, id, replyNote(it.Message.ReplyCount), opts)
	item.Details = bodyLines(render.ResolveMentionsForDisplay(it.Message.Content, resolvers))
	return item
}

// laterStateOrder fixes the section order and friendly headings for Later.
var laterStateOrder = []struct{ key, heading string }{
	{"in_progress", "In progress"},
	{"completed", "Completed"},
	{"archived", "Archived"},
}

// laterSections orders the per-state buckets into sections and builds the summary
// suffix (" · 3 in progress · 1 completed"). Pure — no client, table-testable.
func laterSections(byState map[string][]render.GroupItem, counts map[string]int) (sections []render.GroupSection, summarySuffix string) {
	var parts []string
	for _, o := range laterStateOrder {
		if n := counts[o.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ToLower(o.heading)))
		}
		if len(byState[o.key]) > 0 {
			sections = append(sections, render.GroupSection{Heading: o.heading, Items: byState[o.key]})
		}
	}
	if len(parts) > 0 {
		summarySuffix = " · " + strings.Join(parts, " · ")
	}
	return sections, summarySuffix
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
	mode, err := parseResolveMode(tflags.resolve)
	if err != nil {
		return err
	}
	draftTexts := make([]string, len(drafts))
	for i, d := range drafts {
		draftTexts[i] = d.Text
	}
	resolvers := digestResolvers(ctx, globals, cc, mode, nil, draftTexts)

	items := make([]render.GroupItem, 0, len(drafts))
	for _, d := range drafts {
		target := slack.ResolveChannelName(ctx, cc.Client, d.ChannelID)
		title := render.Emphasize(d.ID, opts.Color) + " → #" + target
		if d.PostAt > 0 {
			title += render.Dim(" · scheduled "+groupedStamp(d.PostAt, opts.Loc), opts.Color)
		}
		details := bodyLines(render.ResolveMentionsForDisplay(d.Text, resolvers))
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
