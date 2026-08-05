package cli

// `message await`: block until one matching event, then print it. The filters
// and projection are shared with `message stream` (message_watch.go); what is
// specific here is the single-conversation scope, the backfill cursor, and the
// timeout-is-not-an-error contract.

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/slack"
)

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
