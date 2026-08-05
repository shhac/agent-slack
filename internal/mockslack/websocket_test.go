package mockslack

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialFake opens the fake socket and returns a reader for its frames.
func dialFake(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), WebSocketURLFor(ts.URL), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var frame map[string]any
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("frame not JSON: %v", err)
	}
	return frame
}

func TestWebSocketReplaysScript(t *testing.T) {
	server := New()
	server.EnableWebSocket(WSScript{Frames: DefaultEventScript()})
	ts := httptest.NewServer(server)
	defer ts.Close()

	conn := dialFake(t, ts)
	for i, want := range DefaultEventScript() {
		got := readFrame(t, conn)
		if got["type"] != want["type"] {
			t.Fatalf("frame %d type = %v, want %v", i, got["type"], want["type"])
		}
	}
}

func TestWebSocketDisabledByDefault(t *testing.T) {
	server := New()
	ts := httptest.NewServer(server)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := websocket.Dial(ctx, WebSocketURLFor(ts.URL), nil); err == nil {
		t.Fatal("want dial failure when no script is installed")
	}
}

func TestWebSocketAnswersPingAndRecordsWrites(t *testing.T) {
	server := New()
	server.EnableWebSocket(WSScript{Frames: []map[string]any{Hello()}, KeepOpen: true})
	ts := httptest.NewServer(server)
	defer ts.Close()

	conn := dialFake(t, ts)
	if got := readFrame(t, conn); got["type"] != "hello" {
		t.Fatalf("first frame = %v, want hello", got["type"])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping","id":7}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	pong := readFrame(t, conn)
	if pong["type"] != "pong" || pong["reply_to"] != float64(7) {
		t.Fatalf("pong = %v", pong)
	}

	// The recorded write is what lets a test assert a client subscribed.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conns := server.WSConnections()
		if len(conns) == 1 && len(conns[0].Sent) == 1 && conns[0].Sent[0]["type"] == "ping" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorded connections = %+v", conns)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGetWebSocketURLBodyPointsAtFakeSocket(t *testing.T) {
	body := GetWebSocketURL("http://127.0.0.1:1234")
	if got := body["primary_websocket_url"]; got != "ws://127.0.0.1:1234/websocket" {
		t.Errorf("primary_websocket_url = %v", got)
	}
	if got := WebSocketURLFor("https://acme.example"); got != "wss://acme.example/websocket" {
		t.Errorf("https should map to wss, got %v", got)
	}
}
