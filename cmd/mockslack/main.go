// mockslack serves fixture responses on Slack's Web API shape for manual
// testing and CLI contract tests:
//
//	mockslack -addr :8765 -fixtures fixtures.json
//
// The fixtures file maps method names to response bodies; an array value is
// consumed as a sequence with the last response sticky:
//
//	{
//	  "auth.test": {"ok": true, "user": "paul"},
//	  "conversations.history": [{"ok": false, "error": "ratelimited"}, {"ok": true, "messages": []}]
//	}
//
// -websocket additionally serves the fake event socket, so a client that opens
// client.getWebSocketURL and connects receives a scripted stream of fabricated
// message/edit/reaction frames:
//
//	mockslack -websocket
//	agent-slack debug ws-capture --duration 30s   # against browser creds pointing here
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "listen address")
	fixtures := flag.String("fixtures", "", "path to fixtures JSON (method -> body or [bodies])")
	expectToken := flag.String("expect-token", "", "reject calls whose token differs (returns invalid_auth)")
	websocket := flag.Bool("websocket", false, "serve the fake event socket and a matching client.getWebSocketURL")
	wsInterval := flag.Duration("websocket-interval", 2*time.Second, "gap between pushed event frames")
	flag.Parse()

	server := mockslack.New()
	server.ExpectToken = *expectToken

	if *fixtures != "" {
		if err := loadFixtures(server, *fixtures); err != nil {
			fmt.Fprintf(os.Stderr, "mockslack: %v\n", err)
			os.Exit(1)
		}
	}

	if *websocket {
		// A manual capture wants a live-looking stream that outlasts the script,
		// so frames are spaced and the socket stays open answering pings.
		server.EnableWebSocket(mockslack.WSScript{
			Frames:   mockslack.DefaultEventScript(),
			Interval: *wsInterval,
			KeepOpen: true,
		})
		server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL("http://"+*addr))
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "mockslack listening on http://%s\n", *addr)
	if err := httpServer.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "mockslack: %v\n", err)
		os.Exit(1)
	}
}

func loadFixtures(server *mockslack.Server, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var byMethod map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byMethod); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for method, value := range byMethod {
		var sequence []any
		if err := json.Unmarshal(value, &sequence); err == nil {
			for _, body := range sequence {
				server.Handle(method, mockslack.Response{Body: body})
			}
			continue
		}
		var single any
		if err := json.Unmarshal(value, &single); err != nil {
			return fmt.Errorf("fixture %q: %w", method, err)
		}
		server.HandleBody(method, single)
	}
	return nil
}
