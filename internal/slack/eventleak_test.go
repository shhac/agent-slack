package slack

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// Every watch run spawns a reader goroutine per connection and a keepalive
// alongside it, and swaps connections under the consumer on reconnect. A leak
// there is invisible in a CLI process that exits immediately, and fatal in the
// MCP server, which is long-lived.
func TestWatchLeavesNoGoroutinesBehind(t *testing.T) {
	// Exclude the harness by IDENTITY, not by function name. A leaked frame
	// reader parks in conn.ReadJSON, whose top frame is
	// internal/poll.runtime_pollWait — ignoring that by name filters out the
	// one goroutine class this test exists to catch.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	exits := map[string]WatchOptions{
		"max-events": {
			Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
			MaxEvents: 1,
			Duration:  3 * time.Second,
			PingEvery: 10 * time.Millisecond,
		},
		"duration": {
			Filter:    EventFilter{Channels: []string{mockslack.WSChannelID}},
			Duration:  150 * time.Millisecond,
			PingEvery: 10 * time.Millisecond,
		},
		"idle-timeout": {
			Filter:      EventFilter{Kinds: []EventKind{EventReactionRemoved}, Channels: []string{"C0FAKEQUIET"}},
			IdleTimeout: 100 * time.Millisecond,
			Duration:    3 * time.Second,
			PingEvery:   10 * time.Millisecond,
		},
		// Reconnect is the only path that swaps connections, spawning a fresh
		// reader and keepalive per attempt and relying on Close to unwind the
		// previous pair — the likeliest place to leak one.
		"reconnect-failed": {
			Filter:    EventFilter{Since: "1700000010.000100", Channels: []string{mockslack.WSChannelID}},
			Duration:  5 * time.Second,
			PingEvery: 10 * time.Millisecond,
		},
	}
	for name, opts := range exits {
		t.Run(name, func(t *testing.T) {
			server := mockslack.New()
			ts := httptest.NewServer(server)
			defer ts.Close()
			script := mockslack.WSScript{Frames: mockslack.DefaultEventScript(), KeepOpen: true}
			if name == "reconnect-failed" {
				// Flap until the budget retires the run.
				script = mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}}
			}
			server.EnableWebSocket(script)
			server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
			server.HandleBody("conversations.history", mockslack.History())
			c := browserClientFor(ts.URL)

			if _, err := Watch(context.Background(), c, opts, func(Event) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Goroutines unwind after the connection closes; give them a moment before
	// the deferred check so a pass means "no leak" rather than "checked early".
	time.Sleep(200 * time.Millisecond)
}
