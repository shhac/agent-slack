package cli

// `message await` and `message stream`: live delivery over the event socket.
// Both share the flag set and the event projection; they differ only in how
// many events they take and how they print them.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// watchPingInterval keeps a long watch's socket alive; Slack closes idle ones.
const watchPingInterval = 30 * time.Second

// watchFlags are the filters both commands share.
type watchFlags struct {
	events               []string
	from                 []string
	reactions            []string
	includeSelf          bool
	excludeBots          bool
	includeThreadReplies bool
	maxBodyChars         int
	resolve              string
	slackMarkdown        bool
	poll                 bool
	pollInterval         time.Duration
}

func (w *watchFlags) bind(cmd *cobra.Command, defaultEvents string) {
	cmd.Flags().StringSliceVar(&w.events, "events", nil,
		"Event kinds to match: message, reaction, edit, delete (default "+defaultEvents+")")
	cmd.Flags().StringSliceVar(&w.from, "from", nil, "Only events authored by these users (@handle, U…, or a bot id); repeatable")
	cmd.Flags().StringSliceVar(&w.reactions, "reaction", nil,
		"Only these reactions (skin tones ignored); repeatable. Non-matching reactions still come back in 'skipped'")
	cmd.Flags().BoolVar(&w.includeSelf, "include-self", false, "Count your own messages")
	cmd.Flags().BoolVar(&w.excludeBots, "exclude-bots", false, "Ignore messages posted by apps")
	cmd.Flags().BoolVar(&w.includeThreadReplies, "include-thread-replies", false,
		"For a channel target, also match replies inside existing threads")
	registerMaxBodyChars(cmd, &w.maxBodyChars, render.DefaultMaxBodyChars, "message")
	cmd.Flags().BoolVar(&w.poll, "poll", false,
		"Read history on an interval instead of the event socket — required for your own DM, which publishes no socket events")
	cmd.Flags().DurationVar(&w.pollInterval, "poll-interval", 0, "How often --poll re-reads history (default 15s)")
	registerSlackMarkdown(cmd, &w.slackMarkdown, true)
	// cached by default: a live stream must not spend an API call per event to
	// expand mentions, so misses stay bare unless the caller opts into fetching.
	registerResolveFlag(cmd, &w.resolve, resolveCached)
	_ = cmd.RegisterFlagCompletionFunc("events", fixedCompletions("message", "reaction", "edit", "delete"))
}

// eventKindAliases keep the CLI vocabulary short: a caller asks for "reaction",
// not for the two wire kinds it expands to.
var eventKindAliases = map[string][]slack.EventKind{
	"message":  {slack.EventMessage},
	"reaction": {slack.EventReactionAdded, slack.EventReactionRemoved},
	"edit":     {slack.EventMessageChanged},
	"delete":   {slack.EventMessageDeleted},
}

func parseEventKinds(values []string) ([]slack.EventKind, error) {
	var kinds []slack.EventKind
	for _, value := range values {
		expanded, ok := eventKindAliases[strings.ToLower(strings.TrimSpace(value))]
		if !ok {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"unknown --events value %q", value).
				WithHint("valid: message, reaction, edit, delete")
		}
		for _, kind := range expanded {
			if !slices.Contains(kinds, kind) {
				kinds = append(kinds, kind)
			}
		}
	}
	return kinds, nil
}

// withReactionKinds makes --reaction mean what it says. Naming a reaction
// while the kind set is messages-only would otherwise filter nothing and match
// the next message instead — the command silently doing something other than
// what was asked.
func withReactionKinds(kinds []slack.EventKind, reactions []string) []slack.EventKind {
	if len(reactions) == 0 {
		return kinds
	}
	for _, kind := range kinds {
		if kind == slack.EventReactionAdded || kind == slack.EventReactionRemoved {
			return kinds
		}
	}
	return append(kinds, slack.EventReactionAdded, slack.EventReactionRemoved)
}

// buildFilter turns the shared flags into the engine's filter. Author handles
// resolve to ids here so the engine stays free of lookups.
func (w *watchFlags) buildFilter(ctx context.Context, cc *clientContext, since string) (slack.EventFilter, error) {
	kinds, err := parseEventKinds(w.events)
	if err != nil {
		return slack.EventFilter{}, err
	}
	from, err := resolveAuthorIDs(ctx, cc, w.from)
	if err != nil {
		return slack.EventFilter{}, err
	}
	reactions, err := normalizeReactionNames(w.reactions)
	if err != nil {
		return slack.EventFilter{}, err
	}
	return slack.EventFilter{
		Kinds:                withReactionKinds(kinds, reactions),
		From:                 from,
		Reactions:            reactions,
		SelfUserID:           selfUserID(cc),
		IncludeSelf:          w.includeSelf,
		ExcludeBots:          w.excludeBots,
		IncludeThreadReplies: w.includeThreadReplies,
		Since:                strings.TrimSpace(since),
	}, nil
}

// resolveAuthorIDs maps --from values to ids. A bot id passes through — apps
// have no user record — but only when it really is one: "Bella" is a handle,
// not a bot, and treating it as one yields a filter that silently never
// matches.
func resolveAuthorIDs(ctx context.Context, cc *clientContext, values []string) ([]string, error) {
	ids := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if render.IsBotID(trimmed) {
			ids = append(ids, trimmed)
			continue
		}
		id, err := slack.ResolveUserID(ctx, cc.Client, trimmed)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// isOwnDM reports whether a conversation is the authenticated user's note-to-
// self DM. Best-effort: an unresolved identity or a failed lookup just means
// the default self-exclusion stands.
func isOwnDM(ctx context.Context, cc *clientContext, channelID string) bool {
	self := selfUserID(cc)
	if self == "" || !strings.HasPrefix(channelID, "D") {
		return false
	}
	own, err := slack.OpenDMChannel(ctx, cc.Client, self)
	return err == nil && own == channelID
}

// selfUserID is the authenticated user, so their own messages don't satisfy an
// await. An unresolved identity just means the exclusion is inert.
func selfUserID(cc *clientContext) string {
	_, userID := slack.IdentityCacheParts(cc.CacheKey)
	return userID
}

// eventRenderer projects events for output, carrying the settings both
// commands share so call sites stay short.
type eventRenderer struct {
	globals       *GlobalFlags
	cc            *clientContext
	mode          resolveMode
	maxBodyChars  int
	slackMarkdown bool
}

func newEventRenderer(globals *GlobalFlags, cc *clientContext, flags *watchFlags) (*eventRenderer, error) {
	mode, err := parseResolveMode(flags.resolve)
	if err != nil {
		return nil, err
	}
	return &eventRenderer{globals: globals, cc: cc, mode: mode, maxBodyChars: flags.maxBodyChars, slackMarkdown: flags.slackMarkdown}, nil
}

// render projects one event and folds in any referenced-entity maps, so every
// emitted line is self-contained — a stream consumer cannot go back for
// context it did not receive.
func (r *eventRenderer) render(ctx context.Context, event slack.Event) compactEvent {
	out := projectEvent(event, r.maxBodyChars, r.slackMarkdown)
	if event.Message == nil {
		return out
	}
	refs := resolveReferencesIn(ctx, r.cc, r.globals, r.mode, true, []render.MessageSummary{*event.Message})
	out.ReferencedUsers, _ = refs["referenced_users"].(map[string]any)
	out.ReferencedChannels, _ = refs["referenced_channels"].(map[string]any)
	out.ReferencedUsergroups, _ = refs["referenced_usergroups"].(map[string]any)
	return out
}

// compactEvent is what await and stream emit. It embeds render.CompactMessage
// so a stream line carries exactly the fields a `message list` line does —
// hand-copying those keys is how forwarded_threads silently went missing — and
// adds the event discriminator plus the fields only an event has.
type compactEvent struct {
	Kind string `json:"event"`
	render.CompactMessage
	// EventTS is when the event happened, present only when that differs from
	// the ts it points at (a reaction's target, an edit's original).
	EventTS         string `json:"event_ts,omitempty"`
	Reaction        string `json:"reaction,omitempty"`
	PreviousContent string `json:"previous_content,omitempty"`
	// The referenced-entity maps read commands attach, so every streamed line
	// is self-contained: a consumer cannot go back for context it did not get.
	ReferencedUsers      map[string]any `json:"referenced_users,omitempty"`
	ReferencedChannels   map[string]any `json:"referenced_channels,omitempty"`
	ReferencedUsergroups map[string]any `json:"referenced_usergroups,omitempty"`
}

// projectEvent renders one event for output. Message bodies go through the
// same compact projection the read commands use, so a stream line and a
// `message list` line describe a message identically — enforced by the embed
// rather than promised in a comment.
func projectEvent(event slack.Event, maxBodyChars int, slackMarkdown bool) compactEvent {
	out := compactEvent{
		Kind:            string(event.Kind),
		EventTS:         event.EventTS,
		Reaction:        event.Reaction,
		PreviousContent: event.PreviousContent,
	}
	if out.EventTS == event.TS {
		out.EventTS = ""
	}
	if event.Message == nil {
		// Reactions and deletes have no message body of their own.
		out.CompactMessage = render.CompactMessage{
			ChannelID: event.ChannelID,
			TS:        event.TS,
			ThreadTS:  event.ThreadTS,
			Author:    event.Author,
		}
		return out
	}
	out.CompactMessage = render.ToCompactMessage(*event.Message, render.CompactOptions{
		MaxBodyChars:     maxBodyChars,
		IncludeReactions: true,
		SlackMarkdown:    slackMarkdown,
	})
	out.ChannelID = event.ChannelID
	return out
}

// pollMode decides socket vs history polling. --poll is an explicit request
// (the only way to watch a conversation the socket stays silent on, such as
// your own DM), and a standard token has no socket to use at all — honest but
// slower, and the caller is told which of the two applies.
func pollMode(globals *GlobalFlags, cc *clientContext, flags *watchFlags, command string) bool {
	if flags.poll {
		return true
	}
	if cc.AuthType == slack.AuthBrowser {
		return false
	}
	emitNotice(globals,
		command+" is polling conversations.history: the event socket needs browser auth",
		"run 'agent-slack auth import-desktop' for live delivery without polling")
	return true
}

// reconnectNotice reports a recovered socket, and says plainly when the gap
// could not be re-read — claiming a gap-fill that did not happen tells the
// caller their stream is intact when events are missing.
func reconnectNotice(globals *GlobalFlags) func(int, bool) {
	return func(attempt int, filled bool) {
		if filled {
			emitNotice(globals, "event socket dropped; reconnected and caught up (attempt "+strconv.Itoa(attempt)+")", "")
			return
		}
		emitNotice(globals,
			"event socket dropped; reconnected but could not catch up (attempt "+strconv.Itoa(attempt)+") — events may be missing",
			"pass --channel to make a stream re-readable after a drop")
	}
}

// resolveWatchTarget maps the CLI target to the conversation (and thread) to
// watch. A permalink or --thread-ts scopes to that thread; a channel target
// watches the channel.
func resolveWatchTarget(ctx context.Context, globals *GlobalFlags, targetInput, threadTS string) (*clientContext, string, string, error) {
	target, err := render.ParseTarget(targetInput)
	if err != nil {
		return nil, "", "", err
	}
	if target.Kind == render.TargetURL {
		warnTruncatedURL(globals, target.Ref)
	}
	cc, channelID, err := resolveTargetClient(ctx, globals, target, "")
	if err != nil {
		return nil, "", "", err
	}
	thread := strings.TrimSpace(threadTS)
	if thread == "" && target.Kind == render.TargetURL {
		// A permalink names a message: await inside its thread, whether it is a
		// thread root or a reply.
		thread = slack.FirstNonEmpty(target.Ref.ThreadTSHint, target.Ref.MessageTS)
	}
	return cc, channelID, thread, nil
}
