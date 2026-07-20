// The short-lived RTM WebSocket the workflow form flow listens on. The
// dialer is injected on Client (like Doer and sleep) so tests fake the
// socket without global state.
package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/coder/websocket"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

type rtmDialer func(ctx context.Context, wsURL, cookie string) (rtmConn, error)

// xoxdCookie is the browser-auth cookie wire format, shared with buildRequest.
func xoxdCookie(xoxd string) string {
	return "d=" + url.QueryEscape(xoxd)
}

// connectRTM opens the event channel: rtm.connect for the socket URL, then
// the injected dialer with the browser cookie attached.
func (c *Client) connectRTM(ctx context.Context) (rtmConn, error) {
	resp, err := c.API(ctx, "rtm.connect", nil)
	if err != nil {
		return nil, err
	}
	wsURL := getStr(resp, "url")
	if wsURL == "" {
		return nil, agenterrors.New("rtm.connect did not return a WebSocket URL", agenterrors.FixableByRetry)
	}
	conn, err := c.dialRTM(ctx, wsURL, xoxdCookie(c.currentAuth().XOXD))
	if err != nil {
		return nil, agenterrors.Wrap(err, agenterrors.FixableByRetry).
			WithHint("could not open the RTM WebSocket — retry")
	}
	return conn, nil
}

// rtmConn's ReadJSON must return once ctx is done — it is the await loop's
// only unblock path.
type rtmConn interface {
	ReadJSON(ctx context.Context) (map[string]any, error)
	Close()
}

func websocketDialRTM(ctx context.Context, wsURL, cookie string) (rtmConn, error) {
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie}},
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(4 << 20)
	return &websocketConn{conn: conn}, nil
}

type websocketConn struct{ conn *websocket.Conn }

func (w *websocketConn) ReadJSON(ctx context.Context) (map[string]any, error) {
	// Non-JSON frames are skipped, not fatal — the loop keeps the interface a
	// plain value-or-error contract. Read(ctx) bounds it.
	for {
		_, data, err := w.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		return msg, nil
	}
}

func (w *websocketConn) Close() { _ = w.conn.Close(websocket.StatusNormalClosure, "") }
