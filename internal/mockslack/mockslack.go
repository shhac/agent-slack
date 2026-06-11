// Package mockslack is a fixture-driven fake of Slack's Web API for tests and
// the cmd/mockslack binary. It is not a Slack clone: it answers POST
// /api/{method} from queued fixture responses and records calls for
// assertions.
package mockslack

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Call records one request for test assertions.
type Call struct {
	Method string
	Params url.Values // form fields (token included for browser-path calls)
	Header http.Header
}

// Response is one canned reply. Zero-value fields default to status 200 and
// an {"ok":true} body.
type Response struct {
	Status int
	Header map[string]string
	Body   any
}

// Server implements http.Handler. Responses queue per method: each call pops
// the next queued response, and the final one is sticky (repeats forever) so
// single-fixture setups behave like a steady-state API.
type Server struct {
	mu     sync.Mutex
	queues map[string][]Response
	calls  []Call

	// ExpectToken, when set, rejects calls whose Bearer or form token differs
	// with Slack's invalid_auth error — exercises auth and refresh paths.
	ExpectToken string
}

func New() *Server {
	return &Server{queues: map[string][]Response{}}
}

// Handle queues responses for a method. Multiple calls append.
func (s *Server) Handle(method string, responses ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queues[method] = append(s.queues[method], responses...)
}

// HandleBody queues a single 200 response with the given JSON body.
func (s *Server) HandleBody(method string, body any) {
	s.Handle(method, Response{Body: body})
}

// Calls returns all recorded calls in order.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

// CallsFor returns recorded calls for one method.
func (s *Server) CallsFor(method string) []Call {
	var out []Call
	for _, c := range s.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// Reset clears queues and recorded calls.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queues = map[string][]Response{}
	s.calls = nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	if r.Method != http.MethodPost || method == "" || method == r.URL.Path {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "unknown_method"})
		return
	}

	// The real API accepts urlencoded and multipart interchangeably.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		_ = r.ParseMultipartForm(32 << 20)
	} else {
		_ = r.ParseForm()
	}

	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: method, Params: cloneValues(r.Form), Header: r.Header.Clone()})
	expectToken := s.ExpectToken
	s.mu.Unlock()

	// Reject before consuming a fixture so a refreshed retry still gets it.
	if expectToken != "" && requestToken(r) != expectToken {
		// Slack reports bad credentials as 200 + ok:false.
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "invalid_auth"})
		return
	}

	s.mu.Lock()
	resp, ok := s.popResponse(method)
	s.mu.Unlock()

	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "unknown_method"})
		return
	}

	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	for k, v := range resp.Header {
		w.Header().Set(k, v)
	}
	body := resp.Body
	if body == nil {
		body = map[string]any{"ok": true}
	}
	writeJSON(w, status, body)
}

// popResponse pops the next queued response, keeping the last one sticky.
// Caller holds s.mu.
func (s *Server) popResponse(method string) (Response, bool) {
	queue := s.queues[method]
	if len(queue) == 0 {
		return Response{}, false
	}
	resp := queue[0]
	if len(queue) > 1 {
		s.queues[method] = queue[1:]
	}
	return resp, true
}

func requestToken(r *http.Request) string {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return bearer
	}
	return r.Form.Get("token")
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
