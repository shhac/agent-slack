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
	cmd.Flags().IntVar(&w.maxBodyChars, "max-body-chars", render.DefaultMaxBodyChars, "Max content chars per message (-1 = unlimited)")
	cmd.Flags().BoolVar(&w.slackMarkdown, "slack-markdown", false, "Render bodies as Slack mrkdwn instead of standard Markdown")
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

// selfUserID is the authenticated user, so their own messages don't satisfy an
// await. The cache key carries it as "<team>/<user>"; an unresolved identity
// just means the exclusion is inert.
func selfUserID(cc *clientContext) string {
	_, userID, _ := strings.Cut(cc.CacheKey, "/")
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
			Content:   event.Content,
		}
		return out
	}
	out.CompactMessage = render.ToCompactMessage(*event.Message, render.CompactOptions{
		MaxBodyChars:     maxBodyChars,
		IncludeReactions: true,
		SlackMarkdown:    slackMarkdown,
	})
	out.CompactMessage.ChannelID = event.ChannelID
	return out
}

// watchAuthMode decides socket vs poll. The socket is a client API, so a
// standard token has to fall back to history reads — honest but slower, and
// the caller is told.
func watchAuthMode(globals *GlobalFlags, cc *clientContext, command string) bool {
	if cc.AuthType == slack.AuthBrowser {
		return false
	}
	emitNotice(globals,
		command+" is polling conversations.history: the event socket needs browser auth",
		"run 'agent-slack auth import-desktop' for live delivery without polling")
	return true
}

func reconnectNotice(globals *GlobalFlags) func(int) {
	return func(attempt int) {
		emitNotice(globals, "event socket dropped; reconnected and gap-filled (attempt "+strconv.Itoa(attempt)+")", "")
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

func registerMessageAwait(parent *cobra.Command, globals *GlobalFlags) {
	flags := &watchFlags{}
	var (
		threadTS string
		since    string
		timeout  time.Duration
	)
	cmd := &cobra.Command{
		Use:   "await <target>",
		Short: "Block until the next message (or reaction) arrives, then print it",
		Long: `Wait for the next matching event in a channel, DM, or thread and print it as
one JSON object. A permalink target awaits inside that message's thread.

Pass --since with the ts of the message you sent, so a reply that arrived
before this command started is still found. --since is exclusive.

A timeout is not an error: it returns {"received": false} with a cursor to
resume from, plus any in-scope events the filters excluded, so a "no" is never
mistaken for silence.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, channelID, thread, err := resolveWatchTarget(ctx, globals, args[0], threadTS)
			if err != nil {
				return err
			}
			filter, err := flags.buildFilter(ctx, cc, since)
			if err != nil {
				return err
			}
			filter.Channels = []string{channelID}
			filter.ThreadTS = thread
			if thread == "" {
				// Watching a conversation: a human answering the message named
				// by --since may reply in-channel or thread on it, so both count.
				filter.RepliesTo = filter.Since
			}
			renderer, err := newEventRenderer(globals, cc, flags)
			if err != nil {
				return err
			}

			result, err := slack.Await(ctx, cc.Client, slack.AwaitOptions{
				Filter:      filter,
				ChannelID:   channelID,
				ThreadTS:    thread,
				Timeout:     timeout,
				Poll:        watchAuthMode(globals, cc, "message await"),
				PingEvery:   watchPingInterval,
				OnReconnect: reconnectNotice(globals),
			})
			if err != nil {
				return err
			}
			return printSingle(globals, awaitPayload(ctx, renderer, result))
		},
	}
	flags.bind(cmd, "message")
	cmd.Flags().StringVar(&threadTS, "thread-ts", "", "Await inside this thread")
	cmd.Flags().StringVar(&since, "since", "", "Only events strictly after this ts (the ts a send returned, or a previous cursor)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "How long to wait before giving up")
	parent.AddCommand(cmd)
}

// awaitOutput is the single JSON resource `message await` prints. Event is the
// same record a stream line carries, so one parser serves both commands.
type awaitOutput struct {
	Received bool          `json:"received"`
	Cursor   string        `json:"cursor,omitempty"`
	WaitedMS int64         `json:"waited_ms"`
	Event    *compactEvent `json:"event,omitempty"`
	// Skipped are in-scope events the filters excluded — the "no" that would
	// otherwise read as silence.
	Skipped          []compactEvent `json:"skipped,omitempty"`
	SkippedTruncated bool           `json:"skipped_truncated,omitempty"`
	Reconnects       int            `json:"reconnects,omitempty"`
}

func awaitPayload(ctx context.Context, renderer *eventRenderer, result slack.AwaitResult) awaitOutput {
	payload := awaitOutput{
		Received:         result.Received,
		Cursor:           result.Cursor,
		WaitedMS:         result.WaitedMS,
		SkippedTruncated: result.SkippedTruncated,
		Reconnects:       result.Reconnects,
	}
	if result.Event != nil {
		event := renderer.render(ctx, *result.Event)
		payload.Event = &event
	}
	for _, event := range result.Skipped {
		payload.Skipped = append(payload.Skipped, renderer.render(ctx, event))
	}
	return payload
}

func registerMessageStream(parent *cobra.Command, globals *GlobalFlags) {
	flags := &watchFlags{}
	var (
		channels    []string
		duration    time.Duration
		idleTimeout time.Duration
		maxEvents   int
	)
	cmd := &cobra.Command{
		Use:   "stream",
		Short: "Stream matching messages and reactions as NDJSON until a bound is reached",
		Long: `Emit live events as NDJSON, one per line, ending with an "@summary" meta line
carrying per-channel cursors.

Without --channel every conversation you can see is streamed. The run is always
bounded: --duration, --max-events, or --idle-timeout. Requires browser auth —
polling every conversation in a workspace is not viable.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			if cc.AuthType != slack.AuthBrowser {
				return agenterrors.New(
					"message stream requires browser auth (xoxc/xoxd); the event socket is a client API",
					agenterrors.FixableByHuman).
					WithHint("import browser credentials with 'agent-slack auth import-desktop', or use 'message await' which can poll")
			}
			filter, err := flags.buildFilter(ctx, cc, "")
			if err != nil {
				return err
			}
			if filter.Channels, err = resolveStreamChannels(ctx, cc, channels); err != nil {
				return err
			}
			renderer, err := newEventRenderer(globals, cc, flags)
			if err != nil {
				return err
			}

			writer, err := streamNDJSON(globals, "message stream")
			if err != nil {
				return err
			}
			result, err := slack.Watch(ctx, cc.Client, slack.WatchOptions{
				Filter:      filter,
				Duration:    duration,
				IdleTimeout: idleTimeout,
				MaxEvents:   maxEvents,
				PingEvery:   watchPingInterval,
				OnReconnect: reconnectNotice(globals),
			}, func(event slack.Event) error {
				return writer.WriteItem(renderer.render(ctx, event))
			})
			if err != nil {
				return err
			}
			return writer.WriteMetaLine("@summary", result)
		},
	}
	flags.bind(cmd, "message")
	cmd.Flags().StringSliceVar(&channels, "channel", nil, "Only these conversations (#name, C…, @handle); repeatable")
	cmd.Flags().DurationVar(&duration, "duration", 10*time.Minute, "Stop after this long (0 = until another bound trips)")
	cmd.Flags().IntVar(&maxEvents, "max-events", 0, "Stop after this many events (0 = no cap)")
	cmd.Flags().DurationVar(&idleTimeout, "idle-timeout", 0, "Stop after this long with no matching event")
	_ = cmd.RegisterFlagCompletionFunc("channel", channelArgCompletion(globals))
	parent.AddCommand(cmd)
}

func resolveStreamChannels(ctx context.Context, cc *clientContext, inputs []string) ([]string, error) {
	ids := make([]string, 0, len(inputs))
	for _, input := range inputs {
		target, err := render.ParseTarget(input)
		if err != nil {
			return nil, err
		}
		if target.Kind == render.TargetUser {
			userID, err := slack.ResolveUserID(ctx, cc.Client, target.UserID)
			if err != nil {
				return nil, err
			}
			channelID, err := slack.OpenDMChannel(ctx, cc.Client, userID)
			if err != nil {
				return nil, err
			}
			ids = append(ids, channelID)
			continue
		}
		channelID, err := slack.ResolveChannelID(ctx, cc.Client, target.Channel)
		if err != nil {
			return nil, err
		}
		ids = append(ids, channelID)
	}
	return ids, nil
}
