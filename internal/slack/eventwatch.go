package slack

// The delivery engine behind `message await` and `message stream`: attach the
// socket, backfill the gap, emit matching events, and survive reconnects.
//
// Ordering is load-bearing. The socket is attached BEFORE the backfill query
// runs, because anything landing between a history response and a completed
// upgrade would otherwise be lost — the same listen-before-act ordering the
// workflow form flow uses. Backfill and live delivery are then reconciled by
// deduping on the event's identity.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// Watch stop reasons. StoppedByDuration/StoppedByCancel/StoppedByClosed are
// shared with the capture loop (events.go) so one vocabulary describes every
// way a socket run can end.
const (
	WatchStoppedMaxEvents = "max-events"
	WatchStoppedIdle      = "idle-timeout"
	// WatchStoppedReconnectFailed means the socket dropped and could not be
	// re-established. It is distinct from a cancellation: the caller did not
	// stop the run, and events may have been missed.
	WatchStoppedReconnectFailed = "reconnect-failed"
)

// WatchOptions configures one watch run.
type WatchOptions struct {
	Filter EventFilter

	// BackfillChannel and BackfillThreadTS name the conversation to catch up on
	// before listening. Only set when the caller has a single target and a
	// cursor — a workspace-wide backfill would fan out unboundedly.
	BackfillChannel  string
	BackfillThreadTS string

	Duration    time.Duration
	IdleTimeout time.Duration
	MaxEvents   int
	PingEvery   time.Duration

	// Poll runs the standard-token fallback: no socket, just repeated history
	// reads. Latency and rate limits are the caller's problem to warn about.
	Poll      bool
	PollEvery time.Duration

	// OnSkipped receives in-scope events the filter excluded, so a caller can
	// tell a rejection from silence.
	OnSkipped func(Event)
	// MaxSkipped bounds how many excluded events are reported (0 = unlimited).
	// Past the bound the cursor stops advancing over them: resuming from a
	// cursor that skipped past unreported rejections would lose them for good,
	// which is the failure the skipped report exists to prevent.
	MaxSkipped int
	// OnReconnect reports a dropped socket that was re-established.
	OnReconnect func(attempt int)
}

// WatchResult summarizes a finished run.
type WatchResult struct {
	// Cursors are per-channel high-water marks: gap-fill is per conversation,
	// so one scalar across channels is not a valid resume point.
	Cursors    map[string]string `json:"cursors,omitempty"`
	Events     int               `json:"events"`
	Reconnects int               `json:"reconnects,omitempty"`
	// Gaps counts reconnects that could not be gap-filled because the run has
	// no explicit channel list to re-read. Non-zero means events may be missing.
	Gaps int `json:"gaps,omitempty"`
	// SkippedTruncated reports that excluded events went unreported because
	// MaxSkipped was reached; the cursor stopped advancing at that point.
	SkippedTruncated bool   `json:"skipped_truncated,omitempty"`
	StoppedBy        string `json:"stopped_by"`
}

const (
	defaultPollEvery = 15 * time.Second
	defaultPingEvery = 30 * time.Second
	// backfillPageLimit is one conversations.history page; backfillMaxMessages
	// bounds a catch-up so a very stale cursor cannot pull an unbounded read.
	backfillPageLimit    = 200
	backfillMaxMessages  = 1000
	maxReconnectAttempts = 20
	reconnectBackoff     = 2 * time.Second
)

// Watch delivers matching events to emit until a bound is reached. emit is
// called synchronously and in order; returning an error from it stops the run.
func Watch(ctx context.Context, c *Client, opts WatchOptions, emit func(Event) error) (WatchResult, error) {
	session := &watchSession{
		client: c,
		opts:   opts,
		emit:   emit,
		seen:   map[string]bool{},
		result: WatchResult{Cursors: map[string]string{}},
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if opts.Duration > 0 {
		var stop context.CancelFunc
		runCtx, stop = context.WithTimeout(runCtx, opts.Duration)
		defer stop()
	}

	if opts.Poll {
		err := session.runPoll(runCtx)
		return session.finish(ctx, runCtx), err
	}
	err := session.runSocket(runCtx)
	return session.finish(ctx, runCtx), err
}

type watchSession struct {
	client *Client
	opts   WatchOptions
	emit   func(Event) error
	seen   map[string]bool
	result WatchResult

	skippedCount int
	// framesSinceReconnect counts what the current socket has delivered, so a
	// reconnect that immediately drops again is not mistaken for a healthy one.
	framesSinceReconnect int
	// reconnectURL is the pre-authorized URL Slack pushes; preferred over a
	// fresh client.getWebSocketURL on reconnect.
	reconnectURL string
}

// finish fills in the stop reason for the deadline cases, which are the only
// ones the run cannot name itself. Everything else records its reason where it
// happens, so a reported reason is true by construction rather than inferred
// after the fact — which is how a dead socket used to be reported as a
// cancellation.
func (s *watchSession) finish(outer, run context.Context) WatchResult {
	if s.result.StoppedBy == "" {
		s.result.StoppedBy = deadlineStopReason(outer, run, s.opts.Duration > 0)
	}
	return s.result
}

// stop records why the run ended, keeping the first reason: the cause is the
// bound that tripped, not whatever unwound afterwards.
func (s *watchSession) stop(reason string) {
	if s.result.StoppedBy == "" {
		s.result.StoppedBy = reason
	}
}

// stopped reports whether a bound has already ended the run.
func (s *watchSession) stopped() bool { return s.result.StoppedBy != "" }

// runSocket is the live path: attach, backfill, then read until a bound trips,
// reconnecting transparently in between.
func (s *watchSession) runSocket(ctx context.Context) error {
	conn, _, err := ConnectEvents(ctx, s.client)
	if err != nil {
		return err
	}
	defer func() { conn.Close() }()

	frames := s.readFrames(ctx, conn)
	if err := s.backfill(ctx); err != nil {
		return err
	}
	// The answer may already have been in the backfill. Without this an await
	// whose event cap is already met still sits out its whole timeout.
	if s.stopped() {
		return nil
	}

	for attempt := 0; ; {
		done, err := s.consume(ctx, frames)
		if err != nil || done {
			return err
		}
		// The frame channel closed: the socket dropped. Reconnect unless the
		// caller gave up on us or we have run out of attempts.
		if ctx.Err() != nil {
			return nil
		}
		if attempt >= maxReconnectAttempts {
			s.stop(WatchStoppedReconnectFailed)
			return nil
		}
		attempt++
		if err := s.client.sleep(ctx, reconnectBackoff); err != nil {
			return nil
		}
		next, gapErr := s.reconnect(ctx, attempt)
		if gapErr != nil {
			// The cursor is still valid, so this is a clean stop — but it is a
			// lost socket, not a cancellation, and the caller must be able to
			// tell those apart to decide whether to resume.
			s.stop(WatchStoppedReconnectFailed)
			return nil
		}
		conn.Close()
		conn = next
		frames = s.readFrames(ctx, conn)
		// A socket that recovered and delivered earns a fresh budget: the cap
		// exists to stop a flapping connection, not to retire a long-lived
		// stream that has dropped and recovered cleanly N times across its
		// duration. A reconnect that goes straight back down does not reset.
		if s.framesSinceReconnect > 0 {
			attempt = 0
		}
		s.framesSinceReconnect = 0
	}
}

// reconnect re-establishes the socket and gap-fills the conversations we know
// about. A run with no explicit channels cannot be gap-filled, so the hole is
// counted and reported rather than papered over.
func (s *watchSession) reconnect(ctx context.Context, attempt int) (rtmConn, error) {
	conn, err := s.redial(ctx)
	if err != nil {
		return nil, err
	}
	s.result.Reconnects++
	if s.opts.OnReconnect != nil {
		s.opts.OnReconnect(attempt)
	}
	channels := s.gapFillChannels()
	if len(channels) == 0 {
		s.result.Gaps++
		return conn, nil
	}
	for _, channelID := range channels {
		// Seed from --since: a run that has not matched anything yet still has
		// a floor to re-read from. Without it the cursor is empty, the catch-up
		// silently reads nothing, and the gap goes unrecorded.
		cursor := maxTS(s.result.Cursors[channelID], s.opts.Filter.Since)
		if cursor == "" {
			s.result.Gaps++
			continue
		}
		if err := s.backfillChannel(ctx, channelID, s.opts.BackfillThreadTS, cursor); err != nil {
			s.result.Gaps++
		}
	}
	return conn, nil
}

// redial prefers the pre-authorized reconnect_url Slack pushed over spending a
// client.getWebSocketURL round trip; a failure there falls back to the normal
// connect, since a stale reconnect URL is not worth ending the run over.
func (s *watchSession) redial(ctx context.Context) (rtmConn, error) {
	if s.reconnectURL != "" {
		conn, err := s.client.dialRTM(ctx, s.reconnectURL, xoxdCookie(s.client.currentAuth().XOXD))
		if err == nil {
			return conn, nil
		}
		s.reconnectURL = ""
		s.client.debugf("reconnect_url dial failed, refetching the socket URL: %v", err)
	}
	conn, _, err := ConnectEvents(ctx, s.client)
	return conn, err
}

func (s *watchSession) gapFillChannels() []string {
	if s.opts.BackfillChannel != "" {
		return []string{s.opts.BackfillChannel}
	}
	return s.opts.Filter.Channels
}

// readFrames pumps the socket into a channel so the backfill can run while the
// socket is already listening. The channel closes on any read error, which the
// consumer reads as "the socket dropped".
func (s *watchSession) readFrames(ctx context.Context, conn rtmConn) <-chan map[string]any {
	out := make(chan map[string]any, 64)
	if s.opts.PingEvery > 0 {
		go pingLoop(ctx, conn, s.opts.PingEvery)
	}
	go func() {
		defer close(out)
		for {
			frame, err := conn.ReadJSON(ctx)
			if err != nil {
				return
			}
			select {
			case out <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// consume drains frames until a bound trips (done=true) or the socket drops
// (done=false, so the caller reconnects).
func (s *watchSession) consume(ctx context.Context, frames <-chan map[string]any) (bool, error) {
	idle := s.newIdleTimer()
	defer idle.Stop()

	for {
		select {
		case <-ctx.Done():
			return true, nil
		case <-idle.C:
			s.stop(WatchStoppedIdle)
			return true, nil
		case frame, ok := <-frames:
			if !ok {
				return false, nil
			}
			s.framesSinceReconnect++
			if getStr(frame, "type") == "reconnect_url" {
				// A pre-authorized URL to reconnect with, so a dropped socket
				// costs no client.getWebSocketURL round trip.
				if url := getStr(frame, "url"); url != "" {
					s.reconnectURL = url
				}
				continue
			}
			event, isEvent := ClassifyFrame(frame)
			if !isEvent {
				continue
			}
			matched, done, err := s.offer(event)
			if err != nil || done {
				return true, err
			}
			// --idle-timeout means "no MATCHING event": on a busy workspace the
			// firehose would otherwise reset it forever and it would never trip.
			if matched {
				s.resetIdle(idle)
			}
		}
	}
}

func (s *watchSession) newIdleTimer() *time.Timer {
	if s.opts.IdleTimeout <= 0 {
		return time.NewTimer(time.Duration(1<<62 - 1))
	}
	return time.NewTimer(s.opts.IdleTimeout)
}

func (s *watchSession) resetIdle(t *time.Timer) {
	if s.opts.IdleTimeout <= 0 {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(s.opts.IdleTimeout)
}

// offer applies dedup and the filter to one classified event, emitting it when
// it matches and reporting it as skipped when it was in scope but excluded.
// matched says whether it reached the caller; done is true once the run's
// event cap is reached.
func (s *watchSession) offer(event Event) (matched, done bool, err error) {
	if s.seen[eventKey(event)] {
		return false, false, nil
	}
	s.seen[eventKey(event)] = true

	if !s.opts.Filter.Matches(event) {
		if s.opts.Filter.InScope(event) && s.notBefore(event) {
			s.reportSkipped(event)
		}
		return false, false, nil
	}

	s.advanceCursor(event)
	if err := s.emit(event); err != nil {
		return false, true, err
	}
	// Counted only after a successful emit: an event the caller never received
	// has not been delivered.
	s.result.Events++
	if s.opts.MaxEvents > 0 && s.result.Events >= s.opts.MaxEvents {
		s.stop(WatchStoppedMaxEvents)
		return true, true, nil
	}
	return true, false, nil
}

// reportSkipped hands an excluded in-scope event to the caller and moves the
// cursor past it — but only while there is room to report it. Once the report
// is full the cursor freezes, so a resumed run re-offers the events the caller
// never saw rather than stepping over a rejection.
func (s *watchSession) reportSkipped(event Event) {
	if s.opts.MaxSkipped > 0 && s.skippedCount >= s.opts.MaxSkipped {
		s.result.SkippedTruncated = true
		return
	}
	s.skippedCount++
	s.advanceCursor(event)
	if s.opts.OnSkipped != nil {
		s.opts.OnSkipped(event)
	}
}

// notBefore keeps stale traffic out of the skipped report: an event from before
// the cursor was never a candidate answer.
func (s *watchSession) notBefore(event Event) bool {
	return s.opts.Filter.Since == "" || tsAfter(event.Cursor(), s.opts.Filter.Since)
}

// advanceCursor moves the channel's high-water mark, never backwards — a
// backfill and the live socket can interleave out of order.
func (s *watchSession) advanceCursor(event Event) {
	s.result.Cursors[event.ChannelID] = maxTS(s.result.Cursors[event.ChannelID], event.Cursor())
}

// eventKey identifies an event for dedup across backfill and live delivery.
func eventKey(e Event) string {
	return string(e.Kind) + "|" + e.ChannelID + "|" + e.TS + "|" + e.EventTS + "|" + e.Reaction + "|" + e.AuthorID()
}

// backfill catches up the single conversation the caller named, so a reply
// that arrived between sending and waiting is not missed.
func (s *watchSession) backfill(ctx context.Context) error {
	if s.opts.BackfillChannel == "" || s.opts.Filter.Since == "" {
		return nil
	}
	return s.backfillChannel(ctx, s.opts.BackfillChannel, s.opts.BackfillThreadTS, s.opts.Filter.Since)
}

// backfillChannel replays history after a cursor through the same offer path
// as live frames, so dedup and filtering behave identically.
func (s *watchSession) backfillChannel(ctx context.Context, channelID, threadTS, since string) error {
	if since == "" {
		return nil
	}
	messages, err := s.fetchSince(ctx, channelID, threadTS, since)
	if err != nil {
		return err
	}
	for _, msg := range messages {
		_, done, emitErr := s.offer(EventFromMessage(channelID, msg))
		if emitErr != nil {
			return emitErr
		}
		if done {
			return nil
		}
	}
	return nil
}

// fetchSince reads a thread's replies or a channel's history after a cursor.
// A thread's replies never appear in channel history unless broadcast, so the
// two cases genuinely need different calls.
func (s *watchSession) fetchSince(ctx context.Context, channelID, threadTS, since string) ([]render.MessageSummary, error) {
	if threadTS != "" {
		replies, err := FetchThread(ctx, s.client, channelID, threadTS, false)
		if err != nil {
			return nil, err
		}
		return afterCursor(replies, since), nil
	}
	messages, err := s.historySince(ctx, channelID, since)
	if err != nil {
		return nil, err
	}
	out := afterCursor(messages, since)

	// A reply threaded on the awaited message is not in channel history unless
	// it was broadcast, so it needs its own read — otherwise the backfill
	// misses the very answer RepliesTo exists to catch.
	repliesTo := s.opts.Filter.RepliesTo
	if repliesTo == "" {
		return out, nil
	}
	// Best-effort, unlike the channel read: --since may be a cursor from an
	// earlier run rather than a message the caller posted, in which case there
	// is no thread to read. Failing the whole await over a speculative fetch
	// would be worse than losing in-thread replies from before it started —
	// the live socket still delivers them from here on.
	replies, err := FetchThread(ctx, s.client, channelID, repliesTo, false)
	if err != nil {
		s.client.debugf("replies backfill for %s skipped: %v", repliesTo, err)
		return out, nil
	}
	return append(out, afterCursor(replies, since)...), nil
}

// historySince reads every message after a cursor, following pages rather than
// stopping at the first. A single page silently truncates a catch-up from a
// stale cursor — the caller would be told it had missed nothing.
func (s *watchSession) historySince(ctx context.Context, channelID, since string) ([]render.MessageSummary, error) {
	var all []render.MessageSummary
	latest := ""
	for len(all) < backfillMaxMessages {
		page, err := FetchChannelHistory(ctx, s.client, HistoryOptions{
			ChannelID: channelID,
			Limit:     backfillPageLimit,
			Oldest:    since,
			Latest:    latest,
		})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return all, nil
		}
		all = append(all, page...)
		if len(page) < backfillPageLimit {
			return all, nil
		}
		// Pages run newest-first; step back from the oldest message we hold.
		// Overlap at the boundary is harmless — dedup collapses it.
		latest = page[0].TS
	}
	// Hitting the cap means older post-cursor messages were not read. That is
	// exactly what Gaps reports: events may be missing.
	s.result.Gaps++
	return all, nil
}

// afterCursor enforces the exclusive semantics of --since: Slack's `oldest` is
// inclusive, and the cursor is usually the caller's own message.
func afterCursor(messages []render.MessageSummary, since string) []render.MessageSummary {
	out := make([]render.MessageSummary, 0, len(messages))
	for _, msg := range messages {
		if tsAfter(msg.TS, since) {
			out = append(out, msg)
		}
	}
	return out
}

// runPoll is the standard-token fallback: no socket, just history reads after
// the cursor. Only usable with a single named conversation.
func (s *watchSession) runPoll(ctx context.Context) error {
	if s.opts.BackfillChannel == "" {
		return agenterrors.New(
			"polling requires a single conversation; the event socket is the only way to watch a whole workspace",
			agenterrors.FixableByHuman).
			WithHint("import browser credentials with 'agent-slack auth import-desktop'")
	}
	every := s.opts.PollEvery
	if every <= 0 {
		every = defaultPollEvery
	}
	cursor, err := s.pollBaseline(ctx)
	if err != nil {
		return err
	}
	for {
		if err := s.backfillChannel(ctx, s.opts.BackfillChannel, s.opts.BackfillThreadTS, cursor); err != nil {
			return err
		}
		if s.stopped() {
			return nil
		}
		cursor = maxTS(cursor, s.result.Cursors[s.opts.BackfillChannel])
		if err := s.client.sleep(ctx, every); err != nil {
			return nil
		}
	}
}

// pollBaseline is where a poll run starts reading from. With no --since there
// is no cursor, and "everything in history" is the wrong answer — the caller
// asked what happens next — so the conversation's current tip becomes the
// baseline.
func (s *watchSession) pollBaseline(ctx context.Context) (string, error) {
	if s.opts.Filter.Since != "" {
		return s.opts.Filter.Since, nil
	}
	messages, err := s.fetchTip(ctx)
	if err != nil {
		return "", err
	}
	if len(messages) == 0 {
		// An empty conversation has no tip to start from, and an empty cursor
		// makes every later read a no-op — the poll would spin until timeout
		// and report silence even as messages arrived. Start from now instead:
		// the caller asked what happens next, and nothing exists before it.
		return nowTS(), nil
	}
	return messages[len(messages)-1].TS, nil
}

func (s *watchSession) fetchTip(ctx context.Context) ([]render.MessageSummary, error) {
	if s.opts.BackfillThreadTS != "" {
		return FetchThread(ctx, s.client, s.opts.BackfillChannel, s.opts.BackfillThreadTS, false)
	}
	return FetchChannelHistory(ctx, s.client, HistoryOptions{ChannelID: s.opts.BackfillChannel, Limit: 1})
}

// nowTS renders the current time as a Slack timestamp, for the one case with
// no message to anchor to.
func nowTS() string {
	now := time.Now()
	return strconv.FormatInt(now.Unix(), 10) + "." + fmt.Sprintf("%06d", now.Nanosecond()/1000)
}
