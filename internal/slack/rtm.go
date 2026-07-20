// The short-lived RTM WebSocket the workflow form flow listens on. The
// dialer is injected on Client (like Doer and sleep) so tests fake the
// socket without global state.
package slack

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
)

type rtmDialer func(ctx context.Context, wsURL, cookie string) (rtmConn, error)

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
	_, data, err := w.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, nil // non-JSON frames are skipped, not fatal
	}
	return msg, nil
}

func (w *websocketConn) Close() { _ = w.conn.Close(websocket.StatusNormalClosure, "") }
