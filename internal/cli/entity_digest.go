package cli

import (
	"context"
	"strings"

	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// Entity digests render channel/user/usergroup list+get under --format
// transcript: a flat listing of Emphasize'd headlines with dim badges, built on
// the shared grouped core (transcriptOpts/writeGrouped/userNameResolver). A
// multi-target get appends a dim "Unresolved" section for misses.

// digestTitle assembles an entity headline: the emphasized label, then any
// badges as a dim ` · `-joined suffix. The shared visual grammar of every
// entity digest item (channels/users/usergroups) so the separator can't drift.
func digestTitle(label string, badges []string, color bool) string {
	title := render.Emphasize(label, color)
	if len(badges) > 0 {
		title += render.Dim(" · "+strings.Join(badges, " · "), color)
	}
	return title
}

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
	resolve := userNameResolver(ctx, cc, counterpartIDs)

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
	label := "#" + slack.FirstNonEmpty(ch.Name, ch.ID)
	switch {
	case ch.IsIM:
		name := resolve(ch.User)
		label = "@" + slack.FirstNonEmpty(name, ch.User, ch.ID) + " (DM)"
	case ch.IsMpim:
		label = slack.FirstNonEmpty(ch.Name, ch.ID) + " (group DM)"
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

	var details []string
	if ch.Topic != "" {
		details = []string{render.Dim(ch.Topic, color)}
	}
	return render.GroupItem{Title: digestTitle(label, badges, color), Details: details}
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
	handle := "@" + slack.FirstNonEmpty(u.Name, u.ID)
	var parts []string
	if name := slack.FirstNonEmpty(u.DisplayName, u.RealName); name != "" {
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
	return render.GroupItem{Title: digestTitle(handle, parts, color)}
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
	label := "@" + slack.FirstNonEmpty(g.Handle, g.ID)
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
	var details []string
	if g.Description != "" {
		details = []string{render.Dim(g.Description, color)}
	}
	return render.GroupItem{Title: digestTitle(label, badges, color), Details: details}
}
