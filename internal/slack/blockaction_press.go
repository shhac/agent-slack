package slack

import (
	"context"
	"reflect"
	"sync/atomic"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
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
	// helloTimeout bounds the wait for RTM's hello before pressing anyway.
	helloTimeout = 3 * time.Second
)

// PressInput is the input to PressMessageAction. Message is the raw message
// the element came from (the pre-press copy a change is judged against);
// Ref locates it for re-reads; Wait bounds how long to watch for the app's
// response once the press returns (0 presses without watching).
type PressInput struct {
	Ref     *render.MessageRef
	Message map[string]any
	Target  render.InteractiveElement
	// Choice is a menu or picker's selection, from ActionChoice.
	Choice map[string]any
	Wait   time.Duration
}

// PressResult reports the app's visible response. Message is the raw
// updated message when it changed; View the raw view it opened. Both can be
// set — Outcome names the most consequential.
type PressResult struct {
	Outcome  string
	Message  map[string]any
	View     map[string]any
	Warnings []string
}

// RequireBlockActionAuth fails unless c can dispatch block actions —
// blocks.actions and rtm.connect are client APIs. It is a local check, so
// callers run it before asking for confirmation.
func RequireBlockActionAuth(c *Client) error {
	if c.currentAuth().Type == AuthBrowser {
		return nil
	}
	return agenterrors.New(
		"pressing a message's buttons requires browser auth (xoxc/xoxd); bot tokens cannot dispatch block actions",
		agenterrors.FixableByHuman).WithHint("import browser credentials with 'agent-slack auth import-desktop'")
}

// PressMessageAction dispatches a block action on a message — what clicking
// the element in Slack does — then watches for the app's response.
//
// Once blocks.actions is sent, nothing is reported as retryable: a retry
// presses again. Every failure after the press is folded into the result
// as a warning instead.
func PressMessageAction(ctx context.Context, c *Client, in PressInput) (PressResult, error) {
	if err := RequireBlockActionAuth(c); err != nil {
		return PressResult{}, err
	}
	params, err := blockActionParams(in)
	if err != nil {
		return PressResult{}, err
	}
	if in.Wait <= 0 {
		if err := dispatchBlockAction(ctx, c, params); err != nil {
			return PressResult{}, err
		}
		return PressResult{Outcome: OutcomeUnobserved}, nil
	}

	conn, err := c.connectRTM(ctx)
	if err != nil {
		return PressResult{}, err
	}
	defer conn.Close()

	listenCtx, stopListening := context.WithCancel(ctx)
	defer stopListening()
	var pressed atomic.Bool
	hello := make(chan struct{})
	frames := make(chan map[string]any, 16)
	go func() {
		defer close(frames)
		helloSeen := false
		for {
			frame, err := conn.ReadJSON(listenCtx)
			if err != nil {
				return
			}
			c.debugJSON("RTM frame", frame)
			if !helloSeen && getStr(frame, "type") == "hello" {
				helloSeen = true
				close(hello)
			}
			// A frame read before the press cannot be its response — an
			// earlier edit of the card, or a form opened on another client.
			if !pressed.Load() || !pressRelated(frame, in) {
				continue
			}
			select {
			case frames <- frame:
			case <-listenCtx.Done():
				return
			}
		}
	}()
	// Pressing before RTM is live would miss a fast app's form.
	select {
	case <-hello:
	case <-time.After(helloTimeout):
	case <-ctx.Done():
		return PressResult{}, ctx.Err()
	}

	pressed.Store(true)
	if err := dispatchBlockAction(ctx, c, params); err != nil {
		return PressResult{}, err
	}
	return observePress(ctx, c, in, frames), nil
}

// dispatchBlockAction sends the press. A transport failure leaves it unknown
// whether Slack received it, so it is not offered as retryable.
func dispatchBlockAction(ctx context.Context, c *Client, params map[string]any) error {
	_, err := c.API(ctx, "blocks.actions", params)
	if err == nil {
		return nil
	}
	var apiErr *agenterrors.APIError
	if agenterrors.As(err, &apiErr) && apiErr.FixableBy == agenterrors.FixableByRetry {
		return agenterrors.Wrap(err, agenterrors.FixableByAgent).
			WithHint("the press may have reached the app — check the message with 'message get' before pressing again")
	}
	return err
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

// pressWatch accumulates what a press's observation has seen.
type pressWatch struct {
	ctx         context.Context
	c           *Client
	in          PressInput
	res         PressResult
	deleted     bool
	misses      int
	readOK      bool
	messageDone bool
	socketLost  bool
}

// observePress watches until in.Wait passes (measured from the press's
// return) or, once a response is seen, a short grace period ends. A card
// change is decided by re-reading the message, not by trusting a frame.
func observePress(ctx context.Context, c *Client, in PressInput, frames <-chan map[string]any) PressResult {
	w := &pressWatch{ctx: ctx, c: c, in: in}
	deadline := time.Now().Add(in.Wait)
	timer := time.NewTimer(in.Wait)
	defer timer.Stop()
	poll := time.NewTicker(pressPollInterval)
	defer poll.Stop()

	settle := func() {
		if until := time.Until(deadline); until > pressGrace {
			deadline = time.Now().Add(pressGrace)
			timer.Reset(pressGrace)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return w.finish()
		case <-timer.C:
			return w.finish()
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				w.socketLost = true
				continue
			}
			if isOpenedView(frame) {
				if w.res.View == nil {
					w.res.View = getRec(frame, "view")
					settle()
				}
				continue
			}
			if w.recheck() {
				settle()
			}
		case <-poll.C:
			if w.recheck() {
				settle()
			}
		}
	}
}

// recheck re-reads the message and reports whether it has changed or gone.
// Not-found must repeat before it counts as a delete: the lookup cascade
// reads a failed fallback call as "not there".
func (w *pressWatch) recheck() bool {
	if w.messageDone {
		return false
	}
	msg, err := findRawMessage(w.ctx, w.c, w.in.Ref, false)
	if err != nil {
		return false
	}
	w.readOK = true
	if msg == nil {
		w.misses++
		if w.misses < 2 {
			return false
		}
		w.deleted, w.messageDone = true, true
		return true
	}
	w.misses = 0
	if messageChanged(w.in.Message, msg) {
		w.res.Message, w.messageDone = msg, true
		return true
	}
	return false
}

func (w *pressWatch) finish() PressResult {
	if !w.messageDone {
		w.recheck()
	}
	switch {
	case w.res.View != nil:
		w.res.Outcome = OutcomeViewOpened
	case w.deleted:
		w.res.Outcome = OutcomeMessageDeleted
	case w.res.Message != nil:
		w.res.Outcome = OutcomeMessageUpdated
	case !w.readOK:
		w.res.Outcome = OutcomeUnknown
	default:
		w.res.Outcome = OutcomeNone
	}
	if w.socketLost && w.res.View == nil {
		w.res.Warnings = append(w.res.Warnings,
			"the RTM socket closed while watching, so a form the app opened would not have been seen")
	}
	if !w.readOK {
		w.res.Warnings = append(w.res.Warnings,
			"could not re-read the message after the press — check it with 'message get' rather than pressing again")
	}
	return w.res
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

func isOpenedView(frame map[string]any) bool {
	t := getStr(frame, "type")
	return t == "view_opened" || t == "view_push"
}
