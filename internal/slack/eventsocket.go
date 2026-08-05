// The event socket: the long-lived WebSocket the Slack web client feeds its
// message pane from. Connecting is a two-step the client performs itself —
// client.getWebSocketURL returns the socket hosts and a routing context, and
// the caller assembles the connect URL with the client's own query params.
//
// Two things read it: the delivery engine behind `message await` / `message
// stream` (eventwatch.go), and the frame capture behind the hidden
// `debug ws-capture`, which exists to learn what a real session sends when
// Slack changes the wire.
package slack

import (
	"context"
	"net/url"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// EventSocket is the client.getWebSocketURL response.
type EventSocket struct {
	PrimaryURL string
	// FallbackURL is the secondary gateway Slack offers. Dialed when the
	// primary refuses, so a single gateway's outage does not end a run.
	FallbackURL    string
	RoutingContext string
}

// eventSocketStartArgs are the connect-time behavior flags, passed as one
// opaque query string the way the web client passes them. connect_only
// suppresses the boot payload — we want events, not a session dump.
const eventSocketStartArgs = "?agent=client&org_wide_aware=true&eac_cache_ts=true&cache_ts=0" +
	"&name_tagging=true&only_self_subteams=true&connect_only=true&ms_latest=true"

// FetchEventSocket resolves the workspace's event socket hosts.
func FetchEventSocket(ctx context.Context, c *Client) (EventSocket, error) {
	if c.currentAuth().Type != AuthBrowser {
		return EventSocket{}, agenterrors.New(
			"the event socket requires browser auth (xoxc/xoxd); standard bot tokens cannot open it",
			agenterrors.FixableByHuman).
			WithHint("import browser credentials with 'agent-slack auth import-desktop'")
	}
	resp, err := c.API(ctx, "client.getWebSocketURL", nil)
	if err != nil {
		return EventSocket{}, err
	}
	socket := EventSocket{
		PrimaryURL:     getStr(resp, "primary_websocket_url"),
		FallbackURL:    getStr(resp, "fallback_websocket_url"),
		RoutingContext: getStr(resp, "routing_context"),
	}
	if socket.PrimaryURL == "" {
		return EventSocket{}, agenterrors.New("client.getWebSocketURL returned no socket URL", agenterrors.FixableByRetry)
	}
	return socket, nil
}

// eventSocketURL assembles the connect URL from the endpoint and the browser
// token, mirroring the web client's parameters. lazy_channels and
// no_query_on_subscribe are the subscription-model flags: with them set the
// server expects the client to declare interest in conversations rather than
// pushing everything, which is exactly what a capture needs to confirm.
func eventSocketURL(socket EventSocket, token string) (string, error) {
	u, err := url.Parse(socket.PrimaryURL)
	if err != nil {
		return "", agenterrors.Wrap(err, agenterrors.FixableByRetry).
			WithHint("client.getWebSocketURL returned an unparseable URL")
	}
	q := url.Values{}
	q.Set("token", token)
	q.Set("sync_desync", "1")
	q.Set("slack_client", "desktop")
	q.Set("start_args", eventSocketStartArgs)
	q.Set("no_query_on_subscribe", "1")
	q.Set("flannel", "3")
	q.Set("lazy_channels", "1")
	q.Set("batch_presence_aware", "1")
	if socket.RoutingContext != "" {
		q.Set("gateway_server", socket.RoutingContext)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ConnectEvents opens the event socket for the current credentials. The
// returned URL is redacted and safe to display.
func ConnectEvents(ctx context.Context, c *Client) (rtmConn, string, error) {
	socket, err := FetchEventSocket(ctx, c)
	if err != nil {
		return nil, "", err
	}
	auth := c.currentAuth()
	cookie := xoxdCookie(auth.XOXD)
	hosts := []string{socket.PrimaryURL}
	if socket.FallbackURL != "" && socket.FallbackURL != socket.PrimaryURL {
		hosts = append(hosts, socket.FallbackURL)
	}

	var lastErr error
	for _, host := range hosts {
		wsURL, err := eventSocketURL(EventSocket{PrimaryURL: host, RoutingContext: socket.RoutingContext}, auth.XOXC)
		if err != nil {
			return nil, "", err
		}
		conn, dialErr := c.dialRTM(ctx, wsURL, cookie)
		if dialErr == nil {
			return conn, redactSecrets(wsURL), nil
		}
		// Slack hands us a second gateway precisely for this; not trying it
		// turns one gateway's outage into a failed run.
		lastErr = dialErr
		c.debugf("event socket dial failed, trying the next gateway: %v", dialErr)
	}
	return nil, "", agenterrors.Wrap(lastErr, agenterrors.FixableByRetry).
		WithHint("could not open the event WebSocket on either gateway — retry")
}
func deadlineStopReason(outer, run context.Context, hasDuration bool) string {
	if outer.Err() == nil && run.Err() != nil && hasDuration {
		return StoppedByDuration
	}
	return StoppedByCancel
}
func pingLoop(ctx context.Context, conn rtmConn, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for id := 1; ; id++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.WriteJSON(ctx, map[string]any{"id": id, "type": "ping"}); err != nil {
				return
			}
		}
	}
}
