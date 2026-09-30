package cli

// `message stream`: emit matching events as NDJSON until a bound trips. The
// filters and projection are shared with `message await` (message_watch.go);
// what is specific here is the multi-channel scope, the browser-auth
// requirement, and the per-channel cursors in the summary.

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerMessageStream(parent *cobra.Command, globals *GlobalFlags) {
	flags := &watchFlags{}
	var (
		channels     []string
		conversation string
		since        string
		duration     time.Duration
		idleTimeout  time.Duration
		maxEvents    int
	)
	cmd := &cobra.Command{
		Use:   "stream",
		Short: "Stream matching messages and reactions as NDJSON until a bound is reached",
		Long: `Emit live events as NDJSON, one per line, ending with an "@summary" meta line
carrying per-channel cursors.

Without --channel every conversation you can see is streamed. The run is always
bounded: --duration, --max-events, or --idle-timeout.

--conversation <ts|permalink> with exactly one --channel follows one
conversation for as long as it runs: replies in its thread, channel-level
messages, and reactions on it or on your own messages in it. It takes --since
(the ts you sent, or an earlier cursor) and catches up from there first. This
is the gapless way to hold a conversation: one socket, no gaps between turns.

Needs browser auth, because the event socket is a client API — unless you pass
--poll with exactly one --channel, which reads that conversation's history on
an interval instead. Polling every conversation in a workspace is not viable,
which is why --poll is opt-in here rather than an automatic fallback.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cc, err := getClient(globals)
			if err != nil {
				return err
			}
			// Explicit only: unlike await, stream never auto-falls-back, because
			// polling every conversation in a workspace is not viable. --poll is
			// how a caller asks for one conversation the socket stays silent on.
			poll := flags.poll
			if !poll && cc.AuthType != slack.AuthBrowser {
				return agenterrors.New(
					"message stream requires browser auth (xoxc/xoxd); the event socket is a client API",
					agenterrors.FixableByHuman).
					WithHint("import browser credentials with 'agent-slack auth import-desktop', or pass --poll --channel <one conversation>")
			}
			if duration <= 0 && maxEvents <= 0 && idleTimeout <= 0 {
				// The help says a run is always bounded, and it must be: an
				// unbounded stream never returns to the agent that spawned it,
				// and its dedup set grows without limit.
				return agenterrors.New("message stream needs a bound", agenterrors.FixableByAgent).
					WithHint("set --duration, --max-events, or --idle-timeout (--duration defaults to 10m)")
			}
			filter, err := flags.buildFilter(ctx, cc, since)
			if err != nil {
				return err
			}
			if filter.Channels, err = resolveStreamChannels(ctx, cc, channels); err != nil {
				return err
			}
			if filter.RepliesTo, err = streamConversation(ctx, cc, filter.Channels, conversation, since); err != nil {
				return err
			}
			// Same reasoning as await: in your own DM every message is yours,
			// so the default self-exclusion would emit nothing — and --poll
			// advertises that conversation as its headline use case.
			if len(filter.Channels) == 1 && isOwnDM(ctx, cc, filter.Channels[0]) {
				filter.IncludeSelf = true
			}
			if poll && len(filter.Channels) != 1 {
				// Polling reads one conversation's history per interval; a
				// workspace-wide poll would be a request storm.
				return agenterrors.New("--poll streams one conversation at a time", agenterrors.FixableByAgent).
					WithHint("pass exactly one --channel, or drop --poll to use the event socket")
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
				Poll:        poll,
				PollEvery:   flags.pollInterval,
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
	cmd.Flags().StringVar(&conversation, "conversation", "",
		"Follow one conversation: ts or permalink of the message that started it (needs exactly one --channel)")
	cmd.Flags().StringVar(&since, "since", "", "With --conversation: catch up on events strictly after this ts first")
	cmd.Flags().DurationVar(&duration, "duration", 10*time.Minute, "Stop after this long (0 = until another bound trips)")
	cmd.Flags().IntVar(&maxEvents, "max-events", 0, "Stop after this many events (0 = no cap)")
	cmd.Flags().DurationVar(&idleTimeout, "idle-timeout", 0, "Stop after this long with no matching event")
	_ = cmd.RegisterFlagCompletionFunc("channel", channelArgCompletion(globals))
	parent.AddCommand(cmd)
}

// streamConversation validates --conversation and --since for a stream. A
// cursor only resumes one conversation — across N channels it would fan out
// into an unbounded catch-up — so both need exactly one --channel.
func streamConversation(ctx context.Context, cc *clientContext, channels []string, conversation, since string) (string, error) {
	conversation = strings.TrimSpace(conversation)
	if conversation == "" {
		if strings.TrimSpace(since) != "" {
			return "", agenterrors.New("--since on a stream needs --conversation", agenterrors.FixableByAgent).
				WithHint("pass --conversation <ts> with one --channel, or drop --since to start live")
		}
		return "", nil
	}
	if len(channels) != 1 {
		return "", agenterrors.New("--conversation follows one conversation, so it needs exactly one --channel",
			agenterrors.FixableByAgent).
			WithHint("pass the channel or DM the conversation is in as the only --channel")
	}
	return resolveConversationRoot(ctx, cc, channels[0], conversation)
}

// resolveStreamChannels maps every --channel value to a conversation id
// through the shared target kernel, so a permalink, #name, id, or @handle all
// mean here what they mean everywhere else. One client serves them all: a
// stream watches a single workspace.
func resolveStreamChannels(ctx context.Context, cc *clientContext, inputs []string) ([]string, error) {
	ids := make([]string, 0, len(inputs))
	for _, input := range inputs {
		target, err := render.ParseTarget(input)
		if err != nil {
			return nil, err
		}
		channelID, err := channelIDForTarget(ctx, cc, target)
		if err != nil {
			return nil, err
		}
		ids = append(ids, channelID)
	}
	return ids, nil
}
