package cli

// `message await`: block until one matching event, then print it. The filters
// and projection are shared with `message stream` (message_watch.go); what is
// specific here is the single-conversation scope, the backfill cursor, and the
// timeout-is-not-an-error contract.

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerMessageAwait(parent *cobra.Command, globals *GlobalFlags) {
	flags := &watchFlags{}
	var (
		threadTS     string
		since        string
		conversation string
		timeout      time.Duration
	)
	cmd := &cobra.Command{
		Use:   "await <target>",
		Short: "Block until the next message (or reaction) arrives, then print it",
		Long: `Wait for the next matching event in a channel, DM, or thread and print it as
one JSON object. A permalink target awaits inside that message's thread.

Pass --since with the ts of the message you sent, so a reply that arrived
before this command started is still found. --since is exclusive.

Holding a conversation over several turns, pass --conversation with the ts of
the message that started it and --since with the previous result's cursor.
Replies threaded on it, channel-level messages, and reactions on it or on your
own messages in it all count.

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
			if filter.RepliesTo, err = awaitRepliesTo(ctx, cc, channelID, thread, conversation, filter.Since); err != nil {
				return err
			}
			// In your own DM every message is yours, so the default
			// self-exclusion would drop all of them and the await would report
			// silence forever. Watching it is only ever a request to see your
			// own writing.
			if isOwnDM(ctx, cc, channelID) {
				filter.IncludeSelf = true
			}
			renderer, err := newEventRenderer(globals, cc, flags)
			if err != nil {
				return err
			}

			result, err := slack.Await(ctx, cc.Client, slack.AwaitOptions{
				Filter:      filter,
				Timeout:     timeout,
				Poll:        pollMode(globals, cc, flags, "message await"),
				PollEvery:   flags.pollInterval,
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
	cmd.Flags().StringVar(&conversation, "conversation", "",
		"Ts or permalink of the message that started the conversation; replies in the channel and in its threads count (default: --since)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "How long to wait before giving up")
	parent.AddCommand(cmd)
}

// awaitRepliesTo picks the conversation root a channel await collects answers
// to. A human answering may reply in-channel or thread on it, so both count.
// Without --conversation it is the --since message: the first turn, where the
// question just sent is the root. From the second turn --since is a cursor, so
// the root has to be named or in-thread replies stop matching.
func awaitRepliesTo(ctx context.Context, cc *clientContext, channelID, thread, conversation, since string) (string, error) {
	if strings.TrimSpace(conversation) == "" {
		if thread != "" {
			return "", nil
		}
		return since, nil
	}
	if thread != "" {
		return "", agenterrors.New("--conversation watches the channel as well as the thread, so it needs a channel target",
			agenterrors.FixableByAgent).
			WithHint("target the channel (e.g. \"#team\" or C…) instead of a permalink, and drop --thread-ts")
	}
	return resolveConversationRoot(ctx, cc, channelID, conversation)
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
	// StoppedBy distinguishes a clean timeout from a lost socket; gaps warns
	// that the answer may have arrived while the connection was down.
	StoppedBy string `json:"stopped_by,omitempty"`
	Gaps      int    `json:"gaps,omitempty"`
}

func awaitPayload(ctx context.Context, renderer *eventRenderer, result slack.AwaitResult) awaitOutput {
	payload := awaitOutput{
		Received:         result.Received,
		Cursor:           result.Cursor,
		WaitedMS:         result.WaitedMS,
		SkippedTruncated: result.SkippedTruncated,
		Reconnects:       result.Reconnects,
		StoppedBy:        result.StoppedBy,
		Gaps:             result.Gaps,
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
