package slack

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
		if d != 60*time.Second {
			t.Errorf("delay %v, want capped 60s", d)
		}
	}
}

// A Retry-After above the old 30s cap but within the new 60s ceiling must be
// honoured verbatim — this is the conversations.history 1 req/min tier case.
func TestRateLimitHonoursHeaderAboveOldCap(t *testing.T) {
	server := mockslack.New()
	server.Handle("conversations.history",
		mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "60"}},
		mockslack.Response{Body: map[string]any{"ok": true}},
	)
	sleep, slept := noSleep(t)
	c := newBrowserClient(t, server, WithSleep(sleep))

	if _, err := c.API(context.Background(), "conversations.history", nil); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] != 60*time.Second {
		t.Errorf("slept = %v, want [60s] honoured verbatim", *slept)
	}
}

func TestRateLimitNotice(t *testing.T) {
	server := mockslack.New()
	server.Handle("conversations.history",
		mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "2"}},
		mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "2"}},
		mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "2"}},
		mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "2"}},
	)
	sleep, _ := noSleep(t)
	var notices []RateLimitNotice
	c := newBrowserClient(t, server, WithSleep(sleep),
		WithRateLimitNotice(func(n RateLimitNotice) { notices = append(notices, n) }))

	if _, err := c.API(context.Background(), "conversations.history", nil); err == nil {
		t.Fatal("expected exhaustion error")
	}

	// One notice per 429: three retried, one terminal.
	if len(notices) != maxRateLimitRetry+1 {
		t.Fatalf("got %d notices, want %d", len(notices), maxRateLimitRetry+1)
	}
	for i, n := range notices[:maxRateLimitRetry] {
		if !n.WillRetry {
			t.Errorf("notice %d: WillRetry = false, want true", i)
		}
		if n.Method != "conversations.history" || n.Attempt != i+1 || n.Delay != 2*time.Second {
			t.Errorf("notice %d = %+v", i, n)
		}
	}
	if last := notices[maxRateLimitRetry]; last.WillRetry {
		t.Errorf("terminal notice WillRetry = true, want false: %+v", last)
	}
}

func TestRetryAfterDuration(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"", 5 * time.Second},      // absent → default
		{"abc", 5 * time.Second},   // unparseable → default
		{"0", 5 * time.Second},     // non-positive → default
		{"-5", 5 * time.Second},    // negative → default
		{"  3  ", 3 * time.Second}, // whitespace trimmed
		{"60", 60 * time.Second},
		{"120", 120 * time.Second}, // capping happens later in call(), not here
	}
	for _, tc := range cases {
		if got := retryAfterDuration(tc.header); got != tc.want {
			t.Errorf("retryAfterDuration(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

// A context error from the backoff sleep (e.g. Retry-After exceeds --timeout)
// must abort immediately with a mapped error and no further request.
func TestRateLimitSleepCancellation(t *testing.T) {
	server := mockslack.New()
	server.Handle("conversations.history",
		mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "2"}},
		mockslack.Response{Body: map[string]any{"ok": true}},
	)
	var notices int
	c := newBrowserClient(t, server,
		WithSleep(func(context.Context, time.Duration) error { return context.DeadlineExceeded }),
		WithRateLimitNotice(func(RateLimitNotice) { notices++ }))

	_, err := c.API(context.Background(), "conversations.history", nil)
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) {
		t.Fatalf("err = %v, want mapped APIError", err)
	}
	if calls := server.CallsFor("conversations.history"); len(calls) != 1 {
		t.Errorf("made %d requests, want 1 (no retry after sleep failure)", len(calls))
	}
	if notices != 1 {
		t.Errorf("notices = %d, want 1 (the 429 fires before the sleep fails)", notices)
	}
}

// The notice must report the server's uncapped ask in RetryAfter and the
// actual capped wait in Delay, so the CLI can show "asked X, waiting Y".
func TestRateLimitNoticeReportsUncappedRetryAfter(t *testing.T) {
	server := mockslack.New()
	server.Handle("conversations.history",
		mockslack.Response{Status: 429, Header: map[string]string{"Retry-After": "120"}},
		mockslack.Response{Body: map[string]any{"ok": true}},
	)
	sleep, _ := noSleep(t)
	var got RateLimitNotice
	c := newBrowserClient(t, server, WithSleep(sleep),
		WithRateLimitNotice(func(n RateLimitNotice) { got = n }))

	if _, err := c.API(context.Background(), "conversations.history", nil); err != nil {
		t.Fatal(err)
	}
	if got.RetryAfter != 120*time.Second || got.Delay != 60*time.Second {
		t.Errorf("notice RetryAfter=%v Delay=%v, want 120s/60s", got.RetryAfter, got.Delay)
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

// Under concurrent auth failures the takeRefresh guard must fire the refresh
// hook exactly once (never a refresh storm), with no data race on the
// mutex-guarded auth/refreshed state. Run with -race.
func TestAuthRefreshConcurrentRefreshesOnce(t *testing.T) {
	server := mockslack.New()
	server.ExpectToken = "xoxb-fresh"
	server.HandleBody("auth.test", map[string]any{"ok": true, "user": "paul"})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	var refreshes int64
	c := New(Auth{Type: AuthStandard, Token: "xoxb-stale"},
		WithBaseURL(ts.URL),
		WithAuthRefresh(func(context.Context) (Auth, bool) {
			atomic.AddInt64(&refreshes, 1)
			return Auth{Type: AuthStandard, Token: "xoxb-fresh"}, true
		}))

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.API(context.Background(), "auth.test", nil)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&refreshes); got != 1 {
		t.Errorf("refreshes = %d, want exactly 1 (takeRefresh guard)", got)
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
