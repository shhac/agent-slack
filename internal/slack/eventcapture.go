package slack

// Frame capture for the hidden `debug ws-capture` command: connect, dump every
// frame, tally what arrived. It exists to learn what a live session sends when
// Slack changes the wire — the shapes the delivery engine relies on were all
// discovered this way — and is deliberately outside the CLI contract.

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strconv"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// CaptureOptions bounds one capture run. A zero Duration and MaxFrames means
// "until the socket closes or the context is cancelled".
type CaptureOptions struct {
	Duration  time.Duration
	MaxFrames int
	// Types, when non-empty, restricts which frame types are emitted. Filtered
	// frames still count toward the summary tally — the point of a capture is
	// learning what arrives, so nothing is silently invisible.
	Types []string
	// Send are frames written once the socket is open, for probing what the
	// server expects (subscriptions, presence queries) without a code change.
	Send      []map[string]any
	PingEvery time.Duration
}

// CaptureFrame is one received frame with its position in the stream.

type CaptureFrame struct {
	Seq       int            `json:"seq"`
	ElapsedMS int64          `json:"elapsed_ms"`
	Type      string         `json:"type"`
	Subtype   string         `json:"subtype,omitempty"`
	Frame     map[string]any `json:"frame"`
}

// CaptureSummary is the tally emitted when a capture finishes.

type CaptureSummary struct {
	SocketURL string         `json:"socket_url"`
	Frames    int            `json:"frames"`
	Emitted   int            `json:"emitted"`
	ByType    map[string]int `json:"by_type"`
	ElapsedMS int64          `json:"elapsed_ms"`
	StoppedBy string         `json:"stopped_by"`
}

// Capture stop reasons.

const (
	StoppedByDuration  = "duration"
	StoppedByMaxFrames = "max-frames"
	StoppedByClosed    = "socket-closed"
	StoppedByCancel    = "cancelled"
)

// CaptureEvents connects, optionally sends probe frames, and hands every
// received frame to emit until the run's bound is reached. Frames are redacted
// before they reach emit, so a capture can never leak the session token into a
// terminal or a file.

func CaptureEvents(ctx context.Context, c *Client, opts CaptureOptions, emit func(CaptureFrame) error) (CaptureSummary, error) {
	conn, socketURL, err := ConnectEvents(ctx, c)
	if err != nil {
		return CaptureSummary{}, err
	}
	defer conn.Close()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if opts.Duration > 0 {
		var stop context.CancelFunc
		runCtx, stop = context.WithTimeout(runCtx, opts.Duration)
		defer stop()
	}

	for _, frame := range opts.Send {
		if err := conn.WriteJSON(runCtx, frame); err != nil {
			return CaptureSummary{}, agenterrors.Wrap(err, agenterrors.FixableByRetry).
				WithHint("the event socket rejected a --send frame")
		}
	}
	if opts.PingEvery > 0 {
		go pingLoop(runCtx, conn, opts.PingEvery)
	}

	started := time.Now()
	summary := CaptureSummary{SocketURL: socketURL, ByType: map[string]int{}}
	wanted := frameTypeFilter(opts.Types)

	for {
		frame, readErr := conn.ReadJSON(runCtx)
		if readErr != nil {
			summary.StoppedBy = stopReason(ctx, runCtx, opts)
			break
		}
		frame = redactFrame(frame)
		frameType := getStr(frame, "type")
		summary.Frames++
		summary.ByType[tallyKey(frame, frameType)]++

		if wanted == nil || wanted[frameType] {
			summary.Emitted++
			if err := emit(CaptureFrame{
				Seq:       summary.Frames,
				ElapsedMS: time.Since(started).Milliseconds(),
				Type:      frameType,
				Subtype:   getStr(frame, "subtype"),
				Frame:     frame,
			}); err != nil {
				return summary, err
			}
		}

		if opts.MaxFrames > 0 && summary.Frames >= opts.MaxFrames {
			summary.StoppedBy = StoppedByMaxFrames
			break
		}
	}

	summary.ElapsedMS = time.Since(started).Milliseconds()
	return summary, nil
}

// deadlineStopReason names the ways a bounded run ends when nothing else
// recorded a reason: the caller cancelled, or the run's own duration expired.
// Shared by the capture and watch loops so one vocabulary describes both.

// stopReason distinguishes the ways a capture read loop ends. A read error
// with both contexts still live means the server hung up.
func stopReason(outer, run context.Context, opts CaptureOptions) string {
	if outer.Err() == nil && run.Err() == nil {
		return StoppedByClosed
	}
	return deadlineStopReason(outer, run, opts.Duration > 0)
}

// tallyKey groups the summary by type, splitting message subtypes out —
// "message/message_changed" behaves nothing like a plain "message", and a
// tally that merges them hides the distinction we are capturing to find.

func tallyKey(frame map[string]any, frameType string) string {
	if frameType == "" {
		frameType = "(none)"
	}
	if subtype := getStr(frame, "subtype"); subtype != "" {
		return frameType + "/" + subtype
	}
	return frameType
}

func frameTypeFilter(types []string) map[string]bool {
	if len(types) == 0 {
		return nil
	}
	set := make(map[string]bool, len(types))
	for _, t := range types {
		set[t] = true
	}
	return set
}

// pingLoop keeps a long capture alive; Slack closes idle sockets.

// SortedTally renders a by-type tally as descending "type=count" pairs, for
// the human-readable end-of-capture line.
func SortedTally(byType map[string]int) []string {
	keys := slices.Collect(maps.Keys(byType))
	sort.Slice(keys, func(i, j int) bool {
		if byType[keys[i]] != byType[keys[j]] {
			return byType[keys[i]] > byType[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+strconv.Itoa(byType[k]))
	}
	return out
}
