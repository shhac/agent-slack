package slack

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// browserClientFor builds a browser-auth client pointed at a mock server. The
// no-op sleep keeps retry and reconnect backoff out of test wall-clock.
func browserClientFor(baseURL string) *Client {
	return New(Auth{Type: AuthBrowser, XOXC: "xoxc-secret", XOXD: "xoxd-secret", WorkspaceURL: baseURL},
		WithSleep(func(ctx context.Context, _ time.Duration) error { return ctx.Err() }))
}

func TestFetchEventSocketRequiresBrowserAuth(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "xoxb-1"})
	_, err := FetchEventSocket(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "browser auth") {
		t.Fatalf("err = %v", err)
	}
}

func TestEventSocketURLCarriesClientParams(t *testing.T) {
	got, err := eventSocketURL(EventSocket{
		PrimaryURL:     "wss://wss-primary.slack.com/",
		RoutingContext: "T_FAKE-1",
	}, "xoxc-secret")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	// The subscription-model flags decide whether messages are pushed at all;
	// dropping one silently changes what the socket delivers.
	for key, want := range map[string]string{
		"token":                 "xoxc-secret",
		"flannel":               "3",
		"lazy_channels":         "1",
		"no_query_on_subscribe": "1",
		"gateway_server":        "T_FAKE-1",
	} {
		if q.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, q.Get(key), want)
		}
	}
	if !strings.Contains(q.Get("start_args"), "connect_only=true") {
		t.Errorf("start_args = %q, want connect_only", q.Get("start_args"))
	}
}

func TestEventSocketURLOmitsEmptyRoutingContext(t *testing.T) {
	got, err := eventSocketURL(EventSocket{PrimaryURL: "wss://wss-primary.slack.com/"}, "xoxc-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "gateway_server") {
		t.Errorf("url = %q, want no gateway_server", got)
	}
}
func TestConnectEventsReturnsRedactedURL(t *testing.T) {
	c := captureFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}, KeepOpen: true})

	conn, socketURL, err := ConnectEvents(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if strings.Contains(socketURL, "xoxc-secret") {
		t.Fatalf("socket URL leaks the token: %q", socketURL)
	}
	if !strings.Contains(socketURL, "token=%5Bredacted%5D") && !strings.Contains(socketURL, "token=[redacted]") {
		t.Errorf("socket URL = %q, want a redacted token param", socketURL)
	}
}
func TestConnectEventsFallsBackToTheSecondGateway(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}, KeepOpen: true})
	server.HandleBody("client.getWebSocketURL", map[string]any{
		"ok": true,
		// Primary points at a host that refuses; the fallback is the live mock.
		"primary_websocket_url":  "ws://127.0.0.1:1/websocket",
		"fallback_websocket_url": mockslack.WebSocketURLFor(ts.URL),
		"routing_context":        "T0FAKETEAM-1",
	})
	c := browserClientFor(ts.URL)

	conn, socketURL, err := ConnectEvents(context.Background(), c)
	if err != nil {
		t.Fatalf("the fallback gateway should have been dialed: %v", err)
	}
	defer conn.Close()
	if !strings.Contains(socketURL, ts.Listener.Addr().String()) {
		t.Errorf("connected to %q, want the fallback host", socketURL)
	}
}

// Both gateways down is a retryable failure, not a silent one.
func TestConnectEventsReportsBothGatewaysDown(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	server.HandleBody("client.getWebSocketURL", map[string]any{
		"ok":                     true,
		"primary_websocket_url":  "ws://127.0.0.1:1/websocket",
		"fallback_websocket_url": "ws://127.0.0.1:2/websocket",
	})
	c := browserClientFor(ts.URL)

	if _, _, err := ConnectEvents(context.Background(), c); err == nil {
		t.Fatal("want an error when no gateway answers")
	}
}
