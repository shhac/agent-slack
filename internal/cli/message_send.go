package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// sendFlags are message send's raw flag values, kept separate so the
// validation matrix (buildSendRequest) is a pure, table-testable function.
type sendFlags struct {
	threadTS       string
	blocksPath     string
	schedule       string
	scheduleIn     string
	attach         []string
	replyBroadcast bool
}

func registerMessageSend(parent *cobra.Command, globals *GlobalFlags) {
	flags := &sendFlags{}
	cmd := &cobra.Command{
		Use:               "send <target> [text]",
		Short:             "Send or schedule a message (channel, #name, U…/DM, or permalink to reply in-thread)",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: targetCompletion(globals),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			text := ""
			if len(args) > 1 {
				text = args[1]
			}
			target, err := render.ParseTarget(args[0])
			if err != nil {
				return err
			}
			if target.Kind == render.TargetURL {
				warnTruncatedURL(globals, target.Ref)
			}
			send, err := buildSendRequest(cmd.InOrStdin(), target.Kind, text, *flags, time.Now())
			if err != nil {
				return err
			}
			cc, channelID, err := resolveTargetClient(ctx, globals, target, "")
			if err != nil {
				return err
			}
			send.channelID = channelID
			if target.Kind == render.TargetURL {
				// Permalink sends always reply in that message's thread.
				send.threadTS, err = threadRootTS(ctx, cc, target.Ref, false)
				if err != nil {
					return err
				}
			}
			return runSend(ctx, globals, cc, send)
		},
	}
	cmd.Flags().StringVar(&flags.threadTS, "thread-ts", "", "Thread root ts to post into")
	cmd.Flags().BoolVar(&flags.replyBroadcast, "reply-broadcast", false, "Broadcast a thread reply to the channel")
	cmd.Flags().StringArrayVar(&flags.attach, "attach", nil, "Attach a local file (repeatable)")
	cmd.Flags().StringVar(&flags.blocksPath, "blocks", "", "Path to a JSON file with Block Kit blocks ('-' = stdin)")
	cmd.Flags().StringVar(&flags.schedule, "schedule", "", "Schedule at an ISO 8601 time with timezone, or a unix timestamp")
	cmd.Flags().StringVar(&flags.scheduleIn, "schedule-in", "", "Schedule after a duration (30m, 2d, tomorrow 9am, monday 9am)")
	parent.AddCommand(cmd)
}

type sendRequest struct {
	channelID      string
	text           string
	rawText        string // original, pre-escape text — for draft rich_text blocks
	blocks         []any
	threadTS       string
	replyBroadcast bool
	attachPaths    []string
	postAt         int64
}

func (req sendRequest) outgoing() slack.OutgoingMessage {
	return slack.OutgoingMessage{
		ChannelID:      req.channelID,
		Text:           req.text,
		RawText:        req.rawText,
		ThreadTS:       req.threadTS,
		ReplyBroadcast: req.replyBroadcast,
		Blocks:         req.blocks,
	}
}

// buildSendRequest validates the flag/target matrix and assembles the
// request: text formatting, list→rich_text conversion or --blocks loading,
// schedule resolution, and the per-target --reply-broadcast rules.
func buildSendRequest(stdin io.Reader, targetKind render.TargetKind, text string, flags sendFlags, now time.Time) (sendRequest, error) {
	if text == "" && len(flags.attach) == 0 && flags.blocksPath == "" {
		return sendRequest{}, agenterrors.New("message text is required unless --attach or --blocks is provided", agenterrors.FixableByAgent)
	}
	postAt, err := slack.ResolveSchedulePostAt(flags.schedule, flags.scheduleIn, now)
	if err != nil {
		return sendRequest{}, err
	}
	attachPaths := dedupeStrings(flags.attach)
	if postAt != 0 && len(attachPaths) > 0 {
		return sendRequest{}, agenterrors.New("--schedule/--schedule-in cannot be combined with --attach (scheduled messages do not support uploads)", agenterrors.FixableByAgent)
	}
	if flags.blocksPath != "" && len(attachPaths) > 0 {
		return sendRequest{}, agenterrors.New("--blocks cannot be combined with --attach", agenterrors.FixableByAgent)
	}

	threadTS := strings.TrimSpace(flags.threadTS)
	switch targetKind {
	case render.TargetUser:
		if flags.replyBroadcast {
			return sendRequest{}, agenterrors.New("--reply-broadcast is not supported for DM targets", agenterrors.FixableByAgent)
		}
	case render.TargetChannel:
		if flags.replyBroadcast && threadTS == "" {
			return sendRequest{}, agenterrors.New("--reply-broadcast requires --thread-ts for channel targets", agenterrors.FixableByAgent)
		}
	}

	var blocks []any
	if flags.blocksPath != "" {
		blocks, err = loadBlocksFromPath(stdin, flags.blocksPath)
		if err != nil {
			return sendRequest{}, err
		}
	} else if text != "" {
		for _, b := range render.TextToRichTextBlocks(text, render.RichTextOptions{}) {
			blocks = append(blocks, b)
		}
	}

	return sendRequest{
		text:           render.FormatOutboundText(text),
		rawText:        text,
		blocks:         blocks,
		threadTS:       threadTS,
		replyBroadcast: flags.replyBroadcast,
		attachPaths:    attachPaths,
		postAt:         postAt,
	}, nil
}

// runSend dispatches to one of three mutually exclusive send modes.
func runSend(ctx context.Context, globals *GlobalFlags, cc *clientContext, req sendRequest) error {
	switch {
	case req.postAt != 0:
		return sendScheduled(ctx, globals, cc, req)
	case len(req.attachPaths) > 0:
		return sendAttachments(ctx, globals, cc, req)
	default:
		return sendPlain(ctx, globals, cc, req)
	}
}

func sendScheduled(ctx context.Context, globals *GlobalFlags, cc *clientContext, req sendRequest) error {
	result, err := slack.ScheduleMessage(ctx, cc.Client, req.outgoing(), req.postAt)
	if err != nil {
		return err
	}
	return printSingle(globals, scheduledPayload(req, result))
}

func sendPlain(ctx context.Context, globals *GlobalFlags, cc *clientContext, req sendRequest) error {
	result, err := slack.PostMessage(ctx, cc.Client, req.outgoing())
	if err != nil {
		return err
	}
	return printSingle(globals, postedPayload(req, result, cc.WorkspaceURL))
}

func sendAttachments(ctx context.Context, globals *GlobalFlags, cc *clientContext, req sendRequest) error {
	if len(req.blocks) > 0 {
		_, _ = fmt.Fprintln(globals.stderr, "Warning: rich text formatting is not supported with file attachments; sending as plain text.")
	}
	initialComment := req.text
	for _, path := range req.attachPaths {
		if err := cc.Client.UploadLocalFile(ctx, req.channelID, path, req.threadTS, initialComment); err != nil {
			return err
		}
		initialComment = ""
	}
	return printSingle(globals, basePayload(req, req.channelID))
}

// basePayload is the success payload every send mode shares.
func basePayload(req sendRequest, channelID string) map[string]any {
	payload := map[string]any{"ok": true, "channel_id": channelID}
	if req.threadTS != "" {
		payload["thread_ts"] = req.threadTS
	}
	return payload
}

func scheduledPayload(req sendRequest, r slack.ScheduleResult) map[string]any {
	payload := basePayload(req, r.ChannelID)
	payload["post_at"] = r.PostAt
	if r.ScheduledMessageID != "" {
		payload["scheduled_message_id"] = r.ScheduledMessageID
	}
	return payload
}

func postedPayload(req sendRequest, r slack.PostResult, workspaceURL string) map[string]any {
	return postedMessagePayload(r, workspaceURL, req.threadTS)
}

// postedMessagePayload is the ok/channel_id/ts/permalink shape every "we posted
// a message" result shares (send and draft send). threadTS, when set, is added
// and woven into the permalink.
func postedMessagePayload(r slack.PostResult, workspaceURL, threadTS string) map[string]any {
	payload := map[string]any{"ok": true, "channel_id": r.ChannelID}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	if r.TS != "" {
		payload["ts"] = r.TS
		if workspaceURL != "" {
			payload["permalink"] = render.BuildMessageURL(render.MessageURLParts{
				WorkspaceURL: workspaceURL,
				ChannelID:    r.ChannelID,
				MessageTS:    r.TS,
				ThreadTS:     threadTS,
			})
		}
	}
	return payload
}

func loadBlocksFromPath(stdin io.Reader, path string) ([]any, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "--blocks: %v", err)
	}
	var parsed []any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "--blocks: expected a JSON array of Block Kit blocks: %v", err)
	}
	for i, el := range parsed {
		if _, ok := el.(map[string]any); !ok {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent, "--blocks: element at index %d is not a Block Kit block object", i)
		}
	}
	return parsed, nil
}

func dedupeStrings(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
