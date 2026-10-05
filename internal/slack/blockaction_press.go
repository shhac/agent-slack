package slack

// Dispatching a press: the blocks.actions call the web client makes when an
// element is clicked, with an RTM listener started first so the app's
// response is not missed. What the response was is judged in
// blockaction_observe.go.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// helloTimeout bounds the wait for RTM's hello before pressing anyway.
const helloTimeout = 3 * time.Second

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

// RequireBlockActionAuth fails unless c can dispatch block actions —
// blocks.actions and rtm.connect are client APIs. It is a local check, so
// callers run it before asking for confirmation.
func RequireBlockActionAuth(c *Client) error {
	return requireBrowserAuth(c, "pressing a message's buttons requires browser auth (xoxc/xoxd); bot tokens cannot dispatch block actions")
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
	listener := listenForPress(listenCtx, c, conn, in)

	// Pressing before RTM is live would miss a fast app's form.
	select {
	case <-listener.hello:
	case <-time.After(helloTimeout):
	case <-ctx.Done():
		return PressResult{}, ctx.Err()
	}

	listener.pressed.Store(true)
	if err := dispatchBlockAction(ctx, c, params); err != nil {
		return PressResult{}, err
	}
	return observePress(ctx, c, in, listener.frames), nil
}

// pressListener is the RTM side of a press: hello closes once the socket is
// live, and frames carries only press-related frames read after pressed is
// set. frames closes when the socket does.
type pressListener struct {
	hello   chan struct{}
	frames  chan map[string]any
	pressed atomic.Bool
}

func listenForPress(ctx context.Context, c *Client, conn rtmConn, in PressInput) *pressListener {
	l := &pressListener{hello: make(chan struct{}), frames: make(chan map[string]any, 16)}
	go func() {
		defer close(l.frames)
		helloSeen := false
		for {
			frame, err := conn.ReadJSON(ctx)
			if err != nil {
				return
			}
			c.debugJSON("RTM frame", frame)
			if !helloSeen && getStr(frame, "type") == "hello" {
				helloSeen = true
				close(l.hello)
			}
			// A frame read before the press cannot be its response — an
			// earlier edit of the card, or a form opened on another client.
			if !l.pressed.Load() || !pressRelated(frame, in) {
				continue
			}
			select {
			case l.frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	return l
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

// blockActionParams builds the blocks.actions form the web client sends: the
// app is addressed by the posting bot (service_id) and its team, the element
// by its block/action ids, and the message by a container like the one Slack
// hands the app in its block_actions payload.
func blockActionParams(in PressInput) (map[string]any, error) {
	serviceID := FirstNonEmpty(getStr(in.Message, "bot_id"), getStr(in.Message, "app_id"))
	if serviceID == "" {
		return nil, agenterrors.New("this message was not posted by an app, so there is nothing listening for the press",
			agenterrors.FixableByAgent).WithHint("only app (bot) messages have pressable buttons")
	}
	actions, _ := json.Marshal([]map[string]any{actionPayload(in.Target, in.Choice)})
	container, _ := json.Marshal(map[string]any{
		"type":         "message",
		"message_ts":   in.Ref.MessageTS,
		"channel_id":   in.Ref.ChannelID,
		"is_ephemeral": false,
	})
	params := map[string]any{
		"service_id":   serviceID,
		"actions":      string(actions),
		"container":    string(container),
		"client_token": clientToken(),
	}
	if team := FirstNonEmpty(getStr(getRec(in.Message, "bot_profile"), "team_id"), getStr(in.Message, "team")); team != "" {
		params["service_team_id"] = team
	}
	return params, nil
}

// actionPayload is the element as the app expects to receive it back: its
// address, type, the fields an app keys behaviour on, and — for a menu or
// picker — the chosen value under the element type's own key.
func actionPayload(ie render.InteractiveElement, chosen map[string]any) map[string]any {
	el := ie.Element
	payload := map[string]any{
		"block_id":  ie.BlockID,
		"action_id": getStr(el, "action_id"),
		"type":      getStr(el, "type"),
	}
	for _, key := range []string{"text", "value", "style", "placeholder"} {
		if v, ok := el[key]; ok {
			payload[key] = v
		}
	}
	for key, v := range chosen {
		payload[key] = v
	}
	return payload
}
