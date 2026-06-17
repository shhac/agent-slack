// Package slack is the HTTP client layer: two transports (browser xoxc+xoxd
// and standard Bearer tokens) behind one Client, bounded 429 retry, error
// mapping to the family's APIError contract, and the resolvers/caches the
// CLI commands share. Everything is dependency-injected (Doer, sleep, base
// URL) so tests run against internal/mockslack without real network access.
//
// The client knows nothing about credential storage, config files, or output
// formatting — those stay in internal/cli and internal/credential.
package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AuthType string

const (
	AuthStandard AuthType = "standard"
	AuthBrowser  AuthType = "browser"
)

// Auth carries the secrets for one workspace. Browser auth posts the xoxc
// token in the form body with the xoxd cookie attached and calls the
// workspace host directly; standard auth sends a Bearer token to the official
// API host.
type Auth struct {
	Type         AuthType
	Token        string // xoxb-/xoxp- (standard)
	XOXC         string // browser token
	XOXD         string // browser cookie
	WorkspaceURL string // required for browser auth
}

// Doer abstracts http.Client for tests.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// RefreshFunc is the auto-refresh seam: invoked at most once per Client when
// a call fails with an auth error. Returning ok=true swaps the credentials
// and retries the failed call. The CLI wires this to Slack Desktop
// re-extraction; the client itself stays storage-agnostic.
type RefreshFunc func(ctx context.Context) (Auth, bool)

const (
	defaultBaseURL    = "https://slack.com"
	defaultTimeout    = 60 * time.Second
	maxRateLimitRetry = 3
	// maxRetryDelay caps how long we honour a server Retry-After. 60s covers
	// Slack's strictest documented tier — the 1 req/min limit on
	// conversations.history / conversations.replies for non-Marketplace apps —
	// while still bounding total wait so a pathological header can't hang the CLI.
	maxRetryDelay = 60 * time.Second
)

// RateLimitNotice describes a single 429 the client received, handed to the
// WithRateLimitNotice hook so the CLI can tell the user a request was throttled.
type RateLimitNotice struct {
	Method     string        // the Slack method that was throttled
	RetryAfter time.Duration // wait the server asked for (or the 5s default)
	Delay      time.Duration // wait we will actually perform (RetryAfter, capped)
	Attempt    int           // 1-based attempt number that hit the limit
	WillRetry  bool          // false once retries are exhausted
}

// RateLimitFunc observes rate-limit hits. It must not block.
type RateLimitFunc func(RateLimitNotice)

type Client struct {
	mu        sync.Mutex
	auth      Auth
	refreshed bool

	doer      Doer
	sleep     func(ctx context.Context, d time.Duration) error
	baseURL   string
	userAgent string
	debug       io.Writer
	onRefresh   RefreshFunc
	onRateLimit RateLimitFunc
	cache       *Cache
}

type Option func(*Client)

func WithDoer(d Doer) Option                { return func(c *Client) { c.doer = d } }
func WithBaseURL(u string) Option           { return func(c *Client) { c.baseURL = strings.TrimSuffix(u, "/") } }
func WithUserAgent(ua string) Option        { return func(c *Client) { c.userAgent = ua } }
func WithDebug(w io.Writer) Option          { return func(c *Client) { c.debug = w } }
func WithAuthRefresh(fn RefreshFunc) Option { return func(c *Client) { c.onRefresh = fn } }
func WithCache(cache *Cache) Option         { return func(c *Client) { c.cache = cache } }

// WithRateLimitNotice registers a hook invoked on every 429, so the caller can
// surface throttling to the user (the client itself stays output-agnostic).
func WithRateLimitNotice(fn RateLimitFunc) Option {
	return func(c *Client) { c.onRateLimit = fn }
}

// WithSleep replaces the retry backoff sleep so tests run without delays.
func WithSleep(fn func(ctx context.Context, d time.Duration) error) Option {
	return func(c *Client) { c.sleep = fn }
}

func New(auth Auth, opts ...Option) *Client {
	c := &Client{
		auth:      auth,
		doer:      &http.Client{Timeout: defaultTimeout},
		baseURL:   defaultBaseURL,
		userAgent: "agent-slack",
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// API calls a Slack Web API method with form-encoded params and returns the
// decoded JSON response. nil param values are dropped; objects and slices are
// JSON-encoded, everything else is stringified (matching the TS client).
func (c *Client) API(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	return c.apiWithRefresh(ctx, method, params, encodeForm)
}

// APIMultipart is API with multipart/form-data encoding. Some internal Slack
// methods (e.g. saved.update) silently ignore urlencoded params.
func (c *Client) APIMultipart(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	return c.apiWithRefresh(ctx, method, params, encodeMultipart)
}

func (c *Client) apiWithRefresh(ctx context.Context, method string, params map[string]any, enc bodyEncoder) (map[string]any, error) {
	resp, err := c.call(ctx, method, params, enc)
	if err == nil || !IsAuthError(err) {
		return resp, err
	}

	fn := c.takeRefresh()
	if fn == nil {
		return nil, err
	}
	newAuth, ok := fn(ctx)
	if !ok {
		return nil, err
	}
	c.setAuth(newAuth)
	c.debugf("auth refreshed, retrying %s", method)
	return c.call(ctx, method, params, enc)
}

// takeRefresh returns the refresh hook the first time an auth error occurs
// and nil afterwards, so a refresh that yields still-bad credentials cannot
// loop.
func (c *Client) takeRefresh() RefreshFunc {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refreshed {
		return nil
	}
	c.refreshed = true
	return c.onRefresh
}

func (c *Client) setAuth(a Auth) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.auth = a
}

func (c *Client) currentAuth() Auth {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.auth
}

func (c *Client) notifyRateLimit(n RateLimitNotice) {
	if c.onRateLimit != nil {
		c.onRateLimit(n)
	}
}

func (c *Client) call(ctx context.Context, method string, params map[string]any, enc bodyEncoder) (map[string]any, error) {
	for attempt := 0; ; attempt++ {
		resp, retryAfter, err := c.doRequest(ctx, method, params, enc)
		if retryAfter <= 0 {
			return resp, err
		}
		willRetry := attempt < maxRateLimitRetry
		delay := min(max(retryAfter, time.Second), maxRetryDelay)
		c.notifyRateLimit(RateLimitNotice{
			Method:     method,
			RetryAfter: retryAfter,
			Delay:      delay,
			Attempt:    attempt + 1,
			WillRetry:  willRetry,
		})
		if !willRetry {
			return resp, err
		}
		c.debugf("429 calling %s, retrying in %s (attempt %d)", method, delay, attempt+1)
		if sleepErr := c.sleep(ctx, delay); sleepErr != nil {
			return nil, mapNetworkError(method, sleepErr)
		}
	}
}

// doRequest performs one HTTP round trip: build the authed request, send it,
// parse the response. retryAfter > 0 signals a 429 the caller may retry; all
// other failures come back fully mapped.
func (c *Client) doRequest(ctx context.Context, method string, params map[string]any, enc bodyEncoder) (map[string]any, time.Duration, error) {
	req, err := c.buildRequest(ctx, method, params, enc)
	if err != nil {
		return nil, 0, err
	}
	httpResp, err := c.doer.Do(req)
	if err != nil {
		return nil, 0, mapNetworkError(method, err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	return c.parseResponse(method, httpResp)
}

// buildRequest assembles the POST for one method under the current auth:
// browser auth posts the xoxc token in the body with the xoxd cookie to the
// workspace host; standard auth sends a Bearer token to the base URL.
func (c *Client) buildRequest(ctx context.Context, method string, params map[string]any, enc bodyEncoder) (*http.Request, error) {
	auth := c.currentAuth()

	fields := map[string]string{}
	for k, v := range params {
		if s, ok := encodeParam(v); ok {
			fields[k] = s
		}
	}

	var endpoint string
	switch auth.Type {
	case AuthBrowser:
		if auth.WorkspaceURL == "" {
			return nil, errBrowserNeedsWorkspace(method)
		}
		endpoint = strings.TrimSuffix(auth.WorkspaceURL, "/") + "/api/" + method
		fields["token"] = auth.XOXC
	default:
		endpoint = c.baseURL + "/api/" + method
	}

	c.debugParams(method, fields)

	body, contentType, err := enc(fields)
	if err != nil {
		return nil, mapNetworkError(method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, mapNetworkError(method, err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", c.userAgent)
	switch auth.Type {
	case AuthBrowser:
		req.Header.Set("Cookie", "d="+url.QueryEscape(auth.XOXD))
		req.Header.Set("Origin", "https://app.slack.com")
	default:
		req.Header.Set("Authorization", "Bearer "+auth.Token)
	}
	return req, nil
}

// parseResponse maps one HTTP response to the (data, retryAfter, error)
// contract: 429s report a retry delay, non-2xx map to HTTP errors, 200 +
// {ok:false} maps to a Slack error, and ok:true logs any soft-failure fields.
func (c *Client) parseResponse(method string, httpResp *http.Response) (map[string]any, time.Duration, error) {
	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, 0, mapNetworkError(method, err)
	}

	if httpResp.StatusCode == http.StatusTooManyRequests {
		c.debugf("POST %s -> 429", method)
		return nil, retryAfterDuration(httpResp.Header.Get("Retry-After")), mapHTTPError(method, httpResp.StatusCode)
	}

	// Slack returns errors as 200 + {ok:false}; an unparseable body collapses
	// to an empty object like the TS client.
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		data = map[string]any{}
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode > 299 {
		c.debugf("POST %s -> %d", method, httpResp.StatusCode)
		return nil, 0, mapHTTPError(method, httpResp.StatusCode)
	}

	if !getBool(data, "ok") {
		code := getStr(data, "error")
		c.debugf("POST %s -> 200 error=%s", method, code)
		c.debugResponse(method, data)
		return nil, 0, mapSlackError(method, code, data)
	}

	// ok:true is not the same as success — some methods (e.g.
	// workflows.triggers.preview) return ok:true with a rejected_triggers /
	// warning field. Surface the real response so --debug can diagnose it.
	c.debugf("POST %s -> 200 ok%s", method, debugSoftFailure(data))
	c.debugResponse(method, data)
	return data, 0, nil
}

func retryAfterDuration(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		seconds = 5
	}
	return time.Duration(seconds) * time.Second
}
