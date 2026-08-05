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
	defer goleak.VerifyNone(t,
		// httptest's own accept loop and the HTTP transport's idle conns are
		// the harness, not the engine under test.
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
	)

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
	}
	for name, opts := range exits {
		t.Run(name, func(t *testing.T) {
			server := mockslack.New()
			ts := httptest.NewServer(server)
			defer ts.Close()
			server.EnableWebSocket(mockslack.WSScript{Frames: mockslack.DefaultEventScript(), KeepOpen: true})
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
