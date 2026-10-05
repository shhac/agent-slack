package slack

// Judging a press's outcome: what the app visibly did in response, decided by
// re-reading the message (RTM message frames only cue an early re-read) and
// by watching for a view the app opened.

import (
	"context"
	"reflect"
	"time"
)

// Press outcomes: what the app visibly did in response.
const (
	OutcomeMessageUpdated = "message_updated"
	OutcomeMessageDeleted = "message_deleted"
	OutcomeViewOpened     = "view_opened"
	OutcomeNone           = "none"
	// OutcomeUnknown: no re-read of the message succeeded, so a change to it
	// cannot be ruled out — the press may still have had an effect.
	OutcomeUnknown = "unknown"
	// OutcomeUnobserved: the caller pressed without watching (Wait 0).
	OutcomeUnobserved = "unobserved"
)

const (
	// pressPollInterval spaces the re-reads that detect a card update; the
	// RTM socket's message frames only hint at one (they are unverified on
	// that socket, and absent for channels the user has not joined).
	pressPollInterval = time.Second
	// pressGrace keeps watching briefly after the first response: an app
	// that updates its card and then opens a form (or the reverse) would
	// otherwise leave that form open on the user's other clients.
	pressGrace = time.Second
)

// PressResult reports the app's visible response. Message is the raw
// updated message when it changed; View the raw view it opened. Both can be
// set — Outcome names the most consequential.
type PressResult struct {
	Outcome  string
	Message  map[string]any
	View     map[string]any
	Warnings []string
}

// pressRelated keeps the frames that can be a press's response: a view the
// message's app opened, or an edit/delete of the message (a cue to re-read).
func pressRelated(frame map[string]any, in PressInput) bool {
	if isOpenedView(frame) {
		appID := FirstNonEmpty(getStr(in.Message, "app_id"), getStr(getRec(in.Message, "bot_profile"), "app_id"))
		viewApp := getStr(getRec(frame, "view"), "app_id")
		return appID == "" || viewApp == "" || appID == viewApp
	}
	if getStr(frame, "type") != "message" || getStr(frame, "channel") != in.Ref.ChannelID {
		return false
	}
	switch getStr(frame, "subtype") {
	case "message_changed":
		return getStr(getRec(frame, "message"), "ts") == in.Ref.MessageTS
	case "message_deleted":
		return getStr(frame, "deleted_ts") == in.Ref.MessageTS
	}
	return false
}

// pressWatch is what a press's observation has seen so far.
type pressWatch struct {
	in         PressInput
	view       map[string]any
	updated    map[string]any
	deleted    bool
	misses     int
	readOK     bool
	socketLost bool
}

// observePress watches until in.Wait passes (measured from the press's
// return) or, once a response is seen, a short grace period ends.
func observePress(ctx context.Context, c *Client, in PressInput, frames <-chan map[string]any) PressResult {
	w := &pressWatch{in: in}
	deadline := time.Now().Add(in.Wait)
	timer := time.NewTimer(in.Wait)
	defer timer.Stop()
	poll := time.NewTicker(pressPollInterval)
	defer poll.Stop()

	settle := func() {
		if time.Until(deadline) > pressGrace {
			deadline = time.Now().Add(pressGrace)
			timer.Reset(pressGrace)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return w.finish(ctx, c)
		case <-timer.C:
			return w.finish(ctx, c)
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				w.socketLost = true
				continue
			}
			if w.onFrame(ctx, c, frame) {
				settle()
			}
		case <-poll.C:
			if w.recheck(ctx, c) {
				settle()
			}
		}
	}
}

// onFrame records a press-related frame and reports whether it revealed a
// response: a view is taken as-is, a message frame only cues a re-read.
func (w *pressWatch) onFrame(ctx context.Context, c *Client, frame map[string]any) bool {
	if !isOpenedView(frame) {
		return w.recheck(ctx, c)
	}
	if w.view != nil {
		return false
	}
	w.view = getRec(frame, "view")
	return true
}

func (w *pressWatch) messageSettled() bool {
	return w.deleted || w.updated != nil
}

// recheck re-reads the message and reports whether it has changed or gone.
// Not-found must repeat before it counts as a delete: the lookup cascade
// reads a failed fallback call as "not there".
func (w *pressWatch) recheck(ctx context.Context, c *Client) bool {
	if w.messageSettled() {
		return false
	}
	msg, err := findRawMessage(ctx, c, w.in.Ref, false)
	if err != nil {
		return false
	}
	w.readOK = true
	if msg == nil {
		w.misses++
		w.deleted = w.misses >= 2
		return w.deleted
	}
	w.misses = 0
	if messageChanged(w.in.Message, msg) {
		w.updated = msg
		return true
	}
	return false
}

// finish takes a last look at the message, then reports.
func (w *pressWatch) finish(ctx context.Context, c *Client) PressResult {
	w.recheck(ctx, c)
	return w.result()
}

// result classifies what was seen.
func (w *pressWatch) result() PressResult {
	res := PressResult{Message: w.updated, View: w.view}
	switch {
	case w.view != nil:
		res.Outcome = OutcomeViewOpened
	case w.deleted:
		res.Outcome = OutcomeMessageDeleted
	case w.updated != nil:
		res.Outcome = OutcomeMessageUpdated
	case !w.readOK:
		res.Outcome = OutcomeUnknown
	default:
		res.Outcome = OutcomeNone
	}
	if w.socketLost && w.view == nil {
		res.Warnings = append(res.Warnings,
			"the RTM socket closed while watching, so a form the app opened would not have been seen")
	}
	if !w.readOK {
		res.Warnings = append(res.Warnings,
			"could not re-read the message after the press — check it with 'message get' rather than pressing again")
	}
	return res
}

// messageChanged compares what a press can change about a card: its text,
// blocks, attachments, and edit stamp. Reactions are left out — another
// user's reaction is not the app's response.
func messageChanged(before, after map[string]any) bool {
	for _, key := range []string{"text", "blocks", "attachments", "edited"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			return true
		}
	}
	return false
}
