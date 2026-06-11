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
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
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
	maxRetryDelay     = 30 * time.Second
)

type Client struct {
	mu        sync.Mutex
	auth      Auth
	refreshed bool

	doer      Doer
	sleep     func(ctx context.Context, d time.Duration) error
	baseURL   string
	userAgent string
	debug     io.Writer
	onRefresh RefreshFunc
}

type Option func(*Client)

func WithDoer(d Doer) Option                { return func(c *Client) { c.doer = d } }
func WithBaseURL(u string) Option           { return func(c *Client) { c.baseURL = strings.TrimSuffix(u, "/") } }
func WithUserAgent(ua string) Option        { return func(c *Client) { c.userAgent = ua } }
func WithDebug(w io.Writer) Option          { return func(c *Client) { c.debug = w } }
func WithAuthRefresh(fn RefreshFunc) Option { return func(c *Client) { c.onRefresh = fn } }

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

type bodyEncoder func(fields map[string]string) (body []byte, contentType string, err error)

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

func (c *Client) call(ctx context.Context, method string, params map[string]any, enc bodyEncoder) (map[string]any, error) {
	for attempt := 0; ; attempt++ {
		resp, retryAfter, err := c.doRequest(ctx, method, params, enc)
		if retryAfter > 0 && attempt < maxRateLimitRetry {
			delay := min(max(retryAfter, time.Second), maxRetryDelay)
			c.debugf("429 calling %s, retrying in %s (attempt %d)", method, delay, attempt+1)
			if sleepErr := c.sleep(ctx, delay); sleepErr != nil {
				return nil, mapNetworkError(method, sleepErr)
			}
			continue
		}
		return resp, err
	}
}

// doRequest performs one HTTP round trip. retryAfter > 0 signals a 429 the
// caller may retry; all other failures come back fully mapped.
func (c *Client) doRequest(ctx context.Context, method string, params map[string]any, enc bodyEncoder) (map[string]any, time.Duration, error) {
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
			return nil, 0, errBrowserNeedsWorkspace(method)
		}
		endpoint = strings.TrimSuffix(auth.WorkspaceURL, "/") + "/api/" + method
		fields["token"] = auth.XOXC
	default:
		endpoint = c.baseURL + "/api/" + method
	}

	c.debugParams(method, fields)

	body, contentType, err := enc(fields)
	if err != nil {
		return nil, 0, mapNetworkError(method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, mapNetworkError(method, err)
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

	httpResp, err := c.doer.Do(req)
	if err != nil {
		return nil, 0, mapNetworkError(method, err)
	}
	defer func() { _ = httpResp.Body.Close() }()

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

	if ok, _ := data["ok"].(bool); !ok {
		code, _ := data["error"].(string)
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

// softFailureKeys are ok:true response fields that nonetheless signal the
// request did not fully succeed.
var softFailureKeys = []string{"rejected_triggers", "warning", "errors", "needed", "provided"}

// debugSoftFailure returns a short " (rejected_triggers, warning)" suffix when
// an ok:true response carries fields that indicate a partial/soft failure.
func debugSoftFailure(data map[string]any) string {
	var present []string
	for _, k := range softFailureKeys {
		if v, ok := data[k]; ok && !isEmptyValue(v) {
			present = append(present, k)
		}
	}
	if len(present) == 0 {
		return ""
	}
	return " soft-failure=" + strings.Join(present, ",")
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

func retryAfterDuration(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		seconds = 5
	}
	return time.Duration(seconds) * time.Second
}

func encodeParam(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

func encodeForm(fields map[string]string) ([]byte, string, error) {
	values := url.Values{}
	for k, v := range fields {
		values.Set(k, v)
	}
	return []byte(values.Encode()), "application/x-www-form-urlencoded", nil
}

func encodeMultipart(fields map[string]string) ([]byte, string, error) {
	var buf strings.Builder
	w := multipart.NewWriter(&buf)
	// Sorted for deterministic bodies (map iteration order is random).
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		if err := w.WriteField(k, fields[k]); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return []byte(buf.String()), w.FormDataContentType(), nil
}

// debugf writes a single-line record to the debug writer.
func (c *Client) debugf(format string, args ...any) {
	if c.debug == nil {
		return
	}
	_, _ = fmt.Fprintf(c.debug, "slack: "+format+"\n", args...)
}

const debugBodyLimit = 2000

// debugRedactKeys are request param keys whose values are secrets.
var debugRedactKeys = map[string]bool{
	"token":       true,
	"xoxc_token":  true,
	"xoxd_cookie": true,
	"cookie":      true,
}

// tokenRe matches any Slack token (xoxc-, xoxb-, xoxp-, xoxd-, …) so it can be
// scrubbed from logged response bodies.
var tokenRe = regexp.MustCompile(`xox[a-zA-Z]-[A-Za-z0-9-]+`)

// debugParams logs the request params with secrets redacted and long values
// truncated. Only called when debug is on.
func (c *Client) debugParams(method string, fields map[string]string) {
	if c.debug == nil {
		return
	}
	parts := make([]string, 0, len(fields))
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		v := fields[k]
		switch {
		case debugRedactKeys[strings.ToLower(k)] || strings.HasPrefix(v, "xox"):
			v = "[redacted]"
		case len(v) > 200:
			v = v[:200] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	c.debugf("POST %s params {%s}", method, strings.Join(parts, " "))
}

// debugResponse logs the parsed response body, token-redacted and truncated.
func (c *Client) debugResponse(method string, data map[string]any) {
	if c.debug == nil {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	s := tokenRe.ReplaceAllString(string(b), "[redacted]")
	if len(s) > debugBodyLimit {
		s = s[:debugBodyLimit] + "…(truncated)"
	}
	c.debugf("POST %s response %s", method, s)
}
