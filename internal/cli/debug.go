package cli

// Development-only commands. Hidden from help, the usage card, and the MCP
// tool surface: these exist to learn what Slack actually sends us, not for
// agents to call. Nothing here is part of the CLI's contract.

import (
	"encoding/json"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/slack"
)

func registerDebug(parent *cobra.Command, globals *GlobalFlags) {
	debugCmd := &cobra.Command{
		Use:    "debug",
		Short:  "Development-only inspection commands (unstable, not part of the CLI contract)",
		Hidden: true,
	}
	parent.AddCommand(debugCmd)
	handleUnknownSubcommand(debugCmd)

	registerDebugWSCapture(debugCmd, globals)
}

func registerDebugWSCapture(parent *cobra.Command, globals *GlobalFlags) {
	var (
		duration  time.Duration
		maxFrames int
		types     []string
		send      []string
		ping      time.Duration
		quiet     bool
	)
	captureCmd := &cobra.Command{
		Use:   "ws-capture",
		Short: "Connect to the Slack event WebSocket and dump every frame received",
		Long: `Open the event socket the Slack web client uses (client.getWebSocketURL)
with the stored browser credentials, and write each received frame as NDJSON
until the duration or frame cap is reached. Ends with an "@summary" line
tallying frame types.

Requires browser auth (xoxc/xoxd) — the socket is a client API.

Tokens are scrubbed from every frame and from the reported socket URL, but the
frames themselves are real workspace traffic: treat captured output as
sensitive and never commit it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Ctrl-C ends the capture and still prints the summary — a capture
			// that loses its tally because the user stopped watching is useless.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			probes, err := parseProbeFrames(send)
			if err != nil {
				return err
			}
			cc, err := getClient(globals)
			if err != nil {
				return err
			}

			writer, err := streamNDJSON(globals, "debug ws-capture")
			if err != nil {
				return err
			}
			emit := func(frame slack.CaptureFrame) error {
				if quiet {
					return nil
				}
				return writer.WriteItem(frame)
			}

			summary, err := slack.CaptureEvents(ctx, cc.Client, slack.CaptureOptions{
				Duration:  duration,
				MaxFrames: maxFrames,
				Types:     types,
				Send:      probes,
				PingEvery: ping,
			}, emit)
			if err != nil {
				return err
			}
			emitNotice(globals,
				"captured "+strings.Join(slack.SortedTally(summary.ByType), " "),
				"stopped by "+summary.StoppedBy)
			return writer.WriteMetaLine("@summary", summary)
		},
	}
	captureCmd.Flags().DurationVar(&duration, "duration", 60*time.Second, "How long to listen (0 = until the socket closes)")
	captureCmd.Flags().IntVar(&maxFrames, "max-frames", 0, "Stop after this many frames (0 = no cap)")
	captureCmd.Flags().StringSliceVar(&types, "type", nil, "Only print these frame types (repeatable); all types still counted in the summary")
	captureCmd.Flags().StringArrayVar(&send, "send", nil, "JSON frame to send once connected (repeatable) — for probing subscriptions")
	captureCmd.Flags().DurationVar(&ping, "ping", 30*time.Second, "Ping interval keeping the socket alive (0 = never)")
	captureCmd.Flags().BoolVar(&quiet, "quiet", false, "Print only the summary, not each frame")
	parent.AddCommand(captureCmd)
}

// parseProbeFrames decodes each --send value as a JSON object.
func parseProbeFrames(values []string) ([]map[string]any, error) {
	frames := make([]map[string]any, 0, len(values))
	for _, value := range values {
		var frame map[string]any
		if err := json.Unmarshal([]byte(value), &frame); err != nil {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"--send must be a JSON object: %v", err).
				WithHint(`example: --send '{"type":"ping","id":1}'`)
		}
		frames = append(frames, frame)
	}
	return frames, nil
}
