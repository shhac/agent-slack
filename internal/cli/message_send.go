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
	"github.com/shhac/agent-slack/internal/output"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerMessageSend(parent *cobra.Command, globals *GlobalFlags) {
	var threadTS, blocksPath, schedule, scheduleIn string
	var attach []string
	var replyBroadcast bool
	cmd := &cobra.Command{
		Use:   "send <target> [text]",
		Short: "Send or schedule a message (channel, #name, U…/DM, or permalink to reply in-thread)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			text := ""
			if len(args) > 1 {
				text = args[1]
			}
			if text == "" && len(attach) == 0 && blocksPath == "" {
				return agenterrors.New("message text is required unless --attach or --blocks is provided", agenterrors.FixableByAgent)
			}

			postAt, err := slack.ResolveSchedulePostAt(schedule, scheduleIn, time.Now())
			if err != nil {
				return err
			}
			attachPaths := dedupeStrings(attach)
			if postAt != 0 && len(attachPaths) > 0 {
				return agenterrors.New("--schedule/--schedule-in cannot be combined with --attach (scheduled messages do not support uploads)", agenterrors.FixableByAgent)
			}
			if blocksPath != "" && len(attachPaths) > 0 {
				return agenterrors.New("--blocks cannot be combined with --attach", agenterrors.FixableByAgent)
			}

			formatted := render.FormatOutboundText(text)
			var blocks []any
			if blocksPath != "" {
				blocks, err = loadBlocksFromPath(cmd.InOrStdin(), blocksPath)
				if err != nil {
					return err
				}
			} else if text != "" {
				for _, b := range render.TextToRichTextBlocks(text, render.RichTextOptions{}) {
					blocks = append(blocks, b)
				}
			}

			target, err := render.ParseTarget(args[0])
			if err != nil {
				return err
			}

			send := sendRequest{
				text:           formatted,
				blocks:         blocks,
				threadTS:       strings.TrimSpace(threadTS),
				replyBroadcast: replyBroadcast,
				attachPaths:    attachPaths,
				postAt:         postAt,
			}

			// Per-target send rules kept caller-side: DM targets reject
			// --reply-broadcast; channel targets need --thread-ts with it.
			switch target.Kind {
			case render.TargetURL:
				warnTruncatedURL(target.Ref)
			case render.TargetUser:
				if replyBroadcast {
					return agenterrors.New("--reply-broadcast is not supported for DM targets", agenterrors.FixableByAgent)
				}
			default:
				if replyBroadcast && send.threadTS == "" {
					return agenterrors.New("--reply-broadcast requires --thread-ts for channel targets", agenterrors.FixableByAgent)
				}
			}
			cc, channelID, err := resolveTargetClient(ctx, globals, target, openDMForUserTargets, "")
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
	cmd.Flags().StringVar(&threadTS, "thread-ts", "", "Thread root ts to post into")
	cmd.Flags().BoolVar(&replyBroadcast, "reply-broadcast", false, "Broadcast a thread reply to the channel")
	cmd.Flags().StringArrayVar(&attach, "attach", nil, "Attach a local file (repeatable)")
	cmd.Flags().StringVar(&blocksPath, "blocks", "", "Path to a JSON file with Block Kit blocks ('-' = stdin)")
	cmd.Flags().StringVar(&schedule, "schedule", "", "Schedule at an ISO 8601 time with timezone, or a unix timestamp")
	cmd.Flags().StringVar(&scheduleIn, "schedule-in", "", "Schedule after a duration (30m, 2d, tomorrow 9am, monday 9am)")
	parent.AddCommand(cmd)
}

type sendRequest struct {
	channelID      string
	text           string
	blocks         []any
	threadTS       string
	replyBroadcast bool
	attachPaths    []string
	postAt         int64
}

func runSend(ctx context.Context, globals *GlobalFlags, cc *clientContext, req sendRequest) error {
	if req.postAt != 0 {
		params := map[string]any{
			"channel": req.channelID,
			"text":    req.text,
			"post_at": req.postAt,
		}
		addThreadParams(params, req)
		resp, err := cc.Client.API(ctx, "chat.scheduleMessage", params)
		if err != nil {
			return err
		}
		channelID := req.channelID
		if id, ok := resp["channel"].(string); ok && id != "" {
			channelID = id
		}
		payload := map[string]any{
			"ok":         true,
			"channel_id": channelID,
			"post_at":    req.postAt,
		}
		if id, ok := resp["scheduled_message_id"].(string); ok {
			payload["scheduled_message_id"] = id
		}
		if at, ok := resp["post_at"].(float64); ok {
			payload["post_at"] = int64(at)
		}
		if req.threadTS != "" {
			payload["thread_ts"] = req.threadTS
		}
		return printSingle(globals, payload)
	}

	if len(req.attachPaths) == 0 {
		params := map[string]any{"channel": req.channelID, "text": req.text}
		addThreadParams(params, req)
		resp, err := cc.Client.API(ctx, "chat.postMessage", params)
		if err != nil {
			return err
		}
		channelID := req.channelID
		if id, ok := resp["channel"].(string); ok && id != "" {
			channelID = id
		}
		payload := map[string]any{"ok": true, "channel_id": channelID}
		if req.threadTS != "" {
			payload["thread_ts"] = req.threadTS
		}
		if ts, ok := resp["ts"].(string); ok && ts != "" {
			payload["ts"] = ts
			if cc.WorkspaceURL != "" {
				payload["permalink"] = render.BuildMessageURL(render.MessageURLParts{
					WorkspaceURL: cc.WorkspaceURL,
					ChannelID:    channelID,
					MessageTS:    ts,
					ThreadTS:     req.threadTS,
				})
			}
		}
		return printSingle(globals, payload)
	}

	if len(req.blocks) > 0 {
		_, _ = fmt.Fprintln(output.Stderr(), "Warning: rich text formatting is not supported with file attachments; sending as plain text.")
	}
	initialComment := req.text
	for _, path := range req.attachPaths {
		if err := cc.Client.UploadLocalFile(ctx, req.channelID, path, req.threadTS, initialComment); err != nil {
			return err
		}
		initialComment = ""
	}
	payload := map[string]any{"ok": true, "channel_id": req.channelID}
	if req.threadTS != "" {
		payload["thread_ts"] = req.threadTS
	}
	return printSingle(globals, payload)
}

func addThreadParams(params map[string]any, req sendRequest) {
	if req.threadTS != "" {
		params["thread_ts"] = req.threadTS
		if req.replyBroadcast {
			params["reply_broadcast"] = true
		}
	}
	if len(req.blocks) > 0 {
		params["blocks"] = req.blocks
	}
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
