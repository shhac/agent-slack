package mockslack

// The fake event socket. Slack's web client feeds its message pane from a
// long-lived WebSocket rather than polling, so anything built on that stream
// needs a server that pushes frames on its own schedule — queued HTTP
// responses cannot model it.
//
// The server upgrades GET /websocket, replays a scripted frame sequence, then
// stays open answering client writes until the caller disconnects. Every frame
// is fabricated (see wsevents.go): shapes are real, ids and content are not.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// WebSocketPath is the upgrade endpoint. Real Slack sockets live on
// wss-primary.slack.com with the token in the query string; the path is ours.
const WebSocketPath = "/websocket"

// WSScript is the fake socket's behavior: frames to push after the upgrade,
// and the gap between them.
type WSScript struct {
	// Frames are pushed in order once the client connects.
	Frames []map[string]any
	// Interval spaces the pushed frames. Zero pushes them back to back, which
	// is what tests want; the mockslack binary spaces them so a manual capture
	// looks like a live stream.
	Interval time.Duration
	// KeepOpen holds the connection after the script is exhausted, answering
	// pings, instead of closing. Manual captures want this; tests do not.
	KeepOpen bool
	// HangUpAfterScript makes the first N connections close once their script
	// is exhausted, and every later one stay up. It models a socket that drops
	// a bounded number of times — a reconnect test that instead lets the fake
	// hang up forever spins as fast as the retry loop allows, which makes
	// anything it asserts about gaps or attempts a race.
	//
	// It takes precedence over KeepOpen, which would otherwise silently void
	// it: "hold every connection open" and "drop the first N" cannot both be
	// true, and the drop is always the more specific request.
	HangUpAfterScript int
}

// EnableWebSocket installs a script on WebSocketPath. Without it the path 404s
// like any other unfixtured route.
func (s *Server) EnableWebSocket(script WSScript) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wsScript = &script
}

// WebSocketURLFor turns an httptest server's base URL into the ws:// URL that
// a client.getWebSocketURL fixture should advertise.
func WebSocketURLFor(baseURL string) string {
	if host, ok := strings.CutPrefix(baseURL, "https://"); ok {
		return "wss://" + host + WebSocketPath
	}
	if host, ok := strings.CutPrefix(baseURL, "http://"); ok {
		return "ws://" + host + WebSocketPath
	}
	return baseURL + WebSocketPath
}

// GetWebSocketURL is a client.getWebSocketURL body pointing at this server's
// own fake socket. TTL mirrors the real response's week.
func GetWebSocketURL(baseURL string) map[string]any {
	return map[string]any{
		"ok":                     true,
		"primary_websocket_url":  WebSocketURLFor(baseURL),
		"fallback_websocket_url": WebSocketURLFor(baseURL),
		"ttl_seconds":            float64(604800),
		"routing_context":        WSTeamID + "-1",
	}
}

// WSConnection records one accepted socket connection for assertions: the
// query the client connected with and every frame it wrote.
type WSConnection struct {
	Query  string
	Cookie string
	Sent   []map[string]any
}

// wsWriter serializes writes to one socket. The script pushes frames on its own
// schedule while the drainer answers pings, and a websocket connection permits
// only one concurrent writer — without this the fixture races under -race and
// can corrupt frames under load.
type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) write(ctx context.Context, frame map[string]any) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.Write(ctx, websocket.MessageText, data)
}

// WSConnections returns the recorded socket connections in order.
func (s *Server) WSConnections() []WSConnection {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WSConnection, len(s.wsConns))
	for i, c := range s.wsConns {
		out[i] = WSConnection{Query: c.Query, Cookie: c.Cookie, Sent: append([]map[string]any(nil), c.Sent...)}
	}
	return out
}

func (s *Server) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	script := *s.wsScript
	s.wsConns = append(s.wsConns, &WSConnection{Query: r.URL.RawQuery, Cookie: r.Header.Get("Cookie")})
	record := s.wsConns[len(s.wsConns)-1]
	connectionCount := len(s.wsConns)
	s.mu.Unlock()
	keepOpen := script.KeepOpen
	if script.HangUpAfterScript > 0 {
		keepOpen = connectionCount > script.HangUpAfterScript
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	ctx := r.Context()
	writer := &wsWriter{conn: conn}
	// Client writes are drained concurrently: the script pushes on its own
	// schedule, and a client that pings mid-script must still be answered.
	// The drainer also tells us when the client went away, which is the only
	// prompt signal for an upgraded connection — the request context is not
	// cancelled when the peer closes, so a handler parked on it alone leaks.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		s.drainWebSocket(ctx, conn, writer, record)
	}()

	for _, frame := range script.Frames {
		if script.Interval > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(script.Interval):
			}
		}
		if err := writer.write(ctx, frame); err != nil {
			return
		}
	}

	if !keepOpen {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return
	}
	select {
	case <-ctx.Done():
	case <-drained:
	}
}

// drainWebSocket records what the client sends and answers pings, so a capture
// that keeps the socket alive behaves the way it would against real Slack.
func (s *Server) drainWebSocket(ctx context.Context, conn *websocket.Conn, writer *wsWriter, record *WSConnection) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		s.mu.Lock()
		record.Sent = append(record.Sent, msg)
		s.mu.Unlock()

		if msg["type"] == "ping" {
			if err := writer.write(ctx, Pong(msg["id"])); err != nil {
				return
			}
		}
	}
}
