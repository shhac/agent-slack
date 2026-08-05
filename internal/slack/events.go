// The event socket: the long-lived WebSocket the Slack web client feeds its
// message pane from. Connecting is a two-step the client performs itself —
// client.getWebSocketURL returns the socket hosts and a routing context, and
// the caller assembles the connect URL with the client's own query params.
//
// This is the read side of a future 'message await' / 'message stream'. Today
// only CaptureEvents consumes it, so we can learn which frames a real session
// actually delivers.
package slack

import (
	"context"
	"maps"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// EventSocket is the client.getWebSocketURL response. TTL is a week, so the
// endpoint is worth caching rather than re-fetching per connect (unlike
// rtm.connect, whose URL is single-use and short-lived).
type EventSocket struct {
	PrimaryURL     string `json:"primary_websocket_url"`
	FallbackURL    string `json:"fallback_websocket_url,omitempty"`
	RoutingContext string `json:"routing_context,omitempty"`
	TTLSeconds     int    `json:"ttl_seconds,omitempty"`
}

// eventSocketStartArgs are the connect-time behavior flags, passed as one
// opaque query string the way the web client passes them. connect_only
// suppresses the boot payload — we want events, not a session dump.
const eventSocketStartArgs = "?agent=client&org_wide_aware=true&eac_cache_ts=true&cache_ts=0" +
	"&name_tagging=true&only_self_subteams=true&connect_only=true&ms_latest=true"

// FetchEventSocket resolves the workspace's event socket hosts.
func FetchEventSocket(ctx context.Context, c *Client) (EventSocket, error) {
	if c.currentAuth().Type != AuthBrowser {
		return EventSocket{}, agenterrors.New(
			"the event socket requires browser auth (xoxc/xoxd); standard bot tokens cannot open it",
			agenterrors.FixableByHuman).
			WithHint("import browser credentials with 'agent-slack auth import-desktop'")
	}
	resp, err := c.API(ctx, "client.getWebSocketURL", nil)
	if err != nil {
		return EventSocket{}, err
	}
	socket := EventSocket{
		PrimaryURL:     getStr(resp, "primary_websocket_url"),
		FallbackURL:    getStr(resp, "fallback_websocket_url"),
		RoutingContext: getStr(resp, "routing_context"),
		TTLSeconds:     int(getNum(resp, "ttl_seconds")),
	}
	if socket.PrimaryURL == "" {
		return EventSocket{}, agenterrors.New("client.getWebSocketURL returned no socket URL", agenterrors.FixableByRetry)
	}
	return socket, nil
}

// eventSocketURL assembles the connect URL from the endpoint and the browser
// token, mirroring the web client's parameters. lazy_channels and
// no_query_on_subscribe are the subscription-model flags: with them set the
// server expects the client to declare interest in conversations rather than
// pushing everything, which is exactly what a capture needs to confirm.
func eventSocketURL(socket EventSocket, token string) (string, error) {
	u, err := url.Parse(socket.PrimaryURL)
	if err != nil {
		return "", agenterrors.Wrap(err, agenterrors.FixableByRetry).
			WithHint("client.getWebSocketURL returned an unparseable URL")
	}
	q := url.Values{}
	q.Set("token", token)
	q.Set("sync_desync", "1")
	q.Set("slack_client", "desktop")
	q.Set("start_args", eventSocketStartArgs)
	q.Set("no_query_on_subscribe", "1")
	q.Set("flannel", "3")
	q.Set("lazy_channels", "1")
	q.Set("batch_presence_aware", "1")
	if socket.RoutingContext != "" {
		q.Set("gateway_server", socket.RoutingContext)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ConnectEvents opens the event socket for the current credentials. The
// returned URL is redacted and safe to display.
func ConnectEvents(ctx context.Context, c *Client) (rtmConn, string, error) {
	socket, err := FetchEventSocket(ctx, c)
	if err != nil {
		return nil, "", err
	}
	auth := c.currentAuth()
	wsURL, err := eventSocketURL(socket, auth.XOXC)
	if err != nil {
		return nil, "", err
	}
	conn, err := c.dialRTM(ctx, wsURL, xoxdCookie(auth.XOXD))
	if err != nil {
		return nil, "", agenterrors.Wrap(err, agenterrors.FixableByRetry).
			WithHint("could not open the event WebSocket — retry")
	}
	return conn, redactSecrets(wsURL), nil
}

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
func deadlineStopReason(outer, run context.Context, hasDuration bool) string {
	if outer.Err() == nil && run.Err() != nil && hasDuration {
		return StoppedByDuration
	}
	return StoppedByCancel
}

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
func pingLoop(ctx context.Context, conn rtmConn, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for id := 1; ; id++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.WriteJSON(ctx, map[string]any{"id": id, "type": "ping"}); err != nil {
				return
			}
		}
	}
}

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
