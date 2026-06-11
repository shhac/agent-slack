package slack

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/mockslack"
)

func noSleep(t *testing.T) (func(ctx context.Context, d time.Duration) error, *[]time.Duration) {
	t.Helper()
	var slept []time.Duration
	return func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}, &slept
}

func newBrowserClient(t *testing.T, server *mockslack.Server, opts ...Option) *Client {
	t.Helper()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	auth := Auth{Type: AuthBrowser, XOXC: "xoxc-test", XOXD: "xoxd-cookie/value", WorkspaceURL: ts.URL}
	return New(auth, opts...)
}

func newStandardClient(t *testing.T, server *mockslack.Server, opts ...Option) *Client {
	t.Helper()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	auth := Auth{Type: AuthStandard, Token: "xoxb-test"}
	return New(auth, append([]Option{WithBaseURL(ts.URL)}, opts...)...)
}

func TestBrowserTransport(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.history", map[string]any{"ok": true, "messages": []any{}})
	c := newBrowserClient(t, server)

	resp, err := c.API(context.Background(), "conversations.history", map[string]any{
		"channel":   "C123",
		"limit":     25,
		"inclusive": true,
		"latest":    nil,                             // dropped
		"metadata":  map[string]any{"include": true}, // JSON-encoded
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Errorf("resp = %v", resp)
	}

	calls := server.CallsFor("conversations.history")
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	call := calls[0]
	if got := call.Params.Get("token"); got != "xoxc-test" {
		t.Errorf("token param = %q", got)
	}
	if got := call.Params.Get("channel"); got != "C123" {
		t.Errorf("channel = %q", got)
	}
	if got := call.Params.Get("limit"); got != "25" {
		t.Errorf("limit = %q", got)
	}
	if got := call.Params.Get("inclusive"); got != "true" {
		t.Errorf("inclusive = %q", got)
	}
	if call.Params.Has("latest") {
		t.Error("nil param should be dropped")
	}
	if got := call.Params.Get("metadata"); got != `{"include":true}` {
		t.Errorf("metadata = %q", got)
	}
	if got := call.Header.Get("Cookie"); !strings.Contains(got, "d=xoxd-cookie%2Fvalue") {
		t.Errorf("cookie = %q, want url-escaped d cookie", got)
	}
	if got := call.Header.Get("Origin"); got != "https://app.slack.com" {
		t.Errorf("origin = %q", got)
	}
	if call.Header.Get("Authorization") != "" {
		t.Error("browser path must not send Authorization")
	}
}

func TestStandardTransport(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul"})
	c := newStandardClient(t, server)

	if _, err := c.API(context.Background(), "auth.test", nil); err != nil {
		t.Fatal(err)
	}
	call := server.CallsFor("auth.test")[0]
	if got := call.Header.Get("Authorization"); got != "Bearer xoxb-test" {
		t.Errorf("authorization = %q", got)
	}
	if call.Params.Has("token") {
		t.Error("standard path must not send token in body")
	}
}

func TestBrowserAuthRequiresWorkspaceURL(t *testing.T) {
	c := New(Auth{Type: AuthBrowser, XOXC: "x", XOXD: "y"})
	_, err := c.API(context.Background(), "auth.test", nil)
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) || apiErr.FixableBy != agenterrors.FixableByHuman {
		t.Fatalf("err = %v, want human-fixable", err)
	}
}

func TestRateLimitRetry(t *testing.T) {
	server := mockslack.New()
	server.Handle("conversations.history",
		mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "2"}},
		mockslack.Response{Status: 429}, // no header → default 5s
		mockslack.Response{Body: map[string]any{"ok": true}},
	)
	sleep, slept := noSleep(t)
	c := newBrowserClient(t, server, WithSleep(sleep))

	if _, err := c.API(context.Background(), "conversations.history", nil); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{2 * time.Second, 5 * time.Second}
	if len(*slept) != 2 || (*slept)[0] != want[0] || (*slept)[1] != want[1] {
		t.Errorf("slept = %v, want %v", *slept, want)
	}
}

func TestRateLimitRetryCapAndExhaustion(t *testing.T) {
	server := mockslack.New()
	server.Handle("m", mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "120"}})
	sleep, slept := noSleep(t)
	c := newBrowserClient(t, server, WithSleep(sleep))

	_, err := c.API(context.Background(), "m", nil)
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) || apiErr.FixableBy != agenterrors.FixableByRetry {
		t.Fatalf("err = %v, want retry-fixable after exhaustion", err)
	}
	if len(*slept) != 3 {
		t.Errorf("retried %d times, want 3", len(*slept))
	}
	for _, d := range *slept {
		if d != 30*time.Second {
			t.Errorf("delay %v, want capped 30s", d)
		}
	}
}

func TestSlackErrorMapping(t *testing.T) {
	cases := []struct {
		code    string
		fixable agenterrors.FixableBy
		isAuth  bool
	}{
		{"invalid_auth", agenterrors.FixableByHuman, true},
		{"token_expired", agenterrors.FixableByHuman, true},
		{"missing_scope", agenterrors.FixableByHuman, false},
		{"ratelimited", agenterrors.FixableByRetry, false},
		{"channel_not_found", agenterrors.FixableByAgent, false},
	}
	for _, tc := range cases {
		server := mockslack.New()
		server.HandleBody("m", map[string]any{"ok": false, "error": tc.code})
		c := newStandardClient(t, server)

		_, err := c.API(context.Background(), "m", nil)
		var apiErr *agenterrors.APIError
		if !agenterrors.As(err, &apiErr) {
			t.Fatalf("%s: err = %v", tc.code, err)
		}
		if apiErr.FixableBy != tc.fixable {
			t.Errorf("%s: fixable = %q, want %q", tc.code, apiErr.FixableBy, tc.fixable)
		}
		if IsAuthError(err) != tc.isAuth {
			t.Errorf("%s: IsAuthError = %v, want %v", tc.code, IsAuthError(err), tc.isAuth)
		}
		if ErrorCode(err) != tc.code {
			t.Errorf("%s: ErrorCode = %q", tc.code, ErrorCode(err))
		}
		if !strings.Contains(apiErr.Message, "calling m") {
			t.Errorf("%s: message %q should name the method", tc.code, apiErr.Message)
		}
	}
}

func TestSlackErrorMetadataHint(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("chat.postMessage", map[string]any{
		"ok":    false,
		"error": "invalid_blocks",
		"response_metadata": map[string]any{
			"messages": []any{"[ERROR] failed to match all allowed schemas [json-pointer:/blocks/0]"},
		},
	})
	c := newStandardClient(t, server)
	_, err := c.API(context.Background(), "chat.postMessage", nil)
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) {
		t.Fatal(err)
	}
	if !strings.Contains(apiErr.Hint, "failed to match") {
		t.Errorf("hint = %q, want response_metadata detail", apiErr.Hint)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	for status, fixable := range map[int]agenterrors.FixableBy{
		500: agenterrors.FixableByRetry,
		401: agenterrors.FixableByHuman,
		400: agenterrors.FixableByAgent,
	} {
		server := mockslack.New()
		server.Handle("m", mockslack.Response{Status: status, Body: map[string]any{}})
		c := newStandardClient(t, server)
		_, err := c.API(context.Background(), "m", nil)
		var apiErr *agenterrors.APIError
		if !agenterrors.As(err, &apiErr) || apiErr.FixableBy != fixable {
			t.Errorf("HTTP %d: err = %v, want %q", status, err, fixable)
		}
	}
}

func TestAuthRefreshHook(t *testing.T) {
	server := mockslack.New()
	server.ExpectToken = "xoxb-fresh"
	server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul"})

	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	refreshes := 0
	c := New(Auth{Type: AuthStandard, Token: "xoxb-stale"},
		WithBaseURL(ts.URL),
		WithAuthRefresh(func(ctx context.Context) (Auth, bool) {
			refreshes++
			return Auth{Type: AuthStandard, Token: "xoxb-fresh"}, true
		}))

	resp, err := c.API(context.Background(), "auth.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if user, _ := resp["user"].(string); user != "paul" {
		t.Errorf("resp = %v", resp)
	}
	if refreshes != 1 {
		t.Errorf("refreshes = %d", refreshes)
	}

	// A second auth failure must not refresh again (loop guard).
	server.ExpectToken = "xoxb-other"
	if _, err := c.API(context.Background(), "auth.test", nil); !IsAuthError(err) {
		t.Fatalf("err = %v, want auth error", err)
	}
	if refreshes != 1 {
		t.Errorf("refreshes = %d after second failure, want still 1", refreshes)
	}
}

func TestAuthRefreshDeclined(t *testing.T) {
	server := mockslack.New()
	server.ExpectToken = "xoxb-good"
	called := false
	c := newStandardClient(t, server, WithAuthRefresh(func(ctx context.Context) (Auth, bool) {
		called = true
		return Auth{}, false
	}))

	_, err := c.API(context.Background(), "auth.test", nil)
	if !IsAuthError(err) {
		t.Fatalf("err = %v", err)
	}
	if !called {
		t.Error("refresh hook should have been consulted")
	}
}

func TestAPIMultipart(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("saved.update", map[string]any{"ok": true})
	c := newBrowserClient(t, server)

	if _, err := c.APIMultipart(context.Background(), "saved.update", map[string]any{"item_id": "C1-123"}); err != nil {
		t.Fatal(err)
	}
	call := server.CallsFor("saved.update")[0]
	if got := call.Header.Get("Content-Type"); !strings.HasPrefix(got, "multipart/form-data") {
		t.Errorf("content-type = %q", got)
	}
	if got := call.Params.Get("item_id"); got != "C1-123" {
		t.Errorf("item_id = %q", got)
	}
	if got := call.Params.Get("token"); got != "xoxc-test" {
		t.Errorf("token = %q", got)
	}
}
