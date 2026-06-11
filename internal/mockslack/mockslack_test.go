package mockslack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func post(t *testing.T, ts *httptest.Server, method string, form url.Values) map[string]any {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/"+method, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSequenceWithStickyLast(t *testing.T) {
	s := New()
	s.Handle("m",
		Response{Body: map[string]any{"ok": true, "n": 1}},
		Response{Body: map[string]any{"ok": true, "n": 2}},
	)
	ts := httptest.NewServer(s)
	defer ts.Close()

	for _, want := range []float64{1, 2, 2, 2} {
		body := post(t, ts, "m", nil)
		if body["n"] != want {
			t.Errorf("n = %v, want %v", body["n"], want)
		}
	}
}

func TestUnknownMethod(t *testing.T) {
	s := New()
	ts := httptest.NewServer(s)
	defer ts.Close()

	body := post(t, ts, "no.such", nil)
	if body["error"] != "unknown_method" {
		t.Errorf("body = %v", body)
	}
}

func TestExpectTokenDoesNotConsumeFixture(t *testing.T) {
	s := New()
	s.ExpectToken = "good"
	s.Handle("m", Response{Body: map[string]any{"ok": true, "kept": true}})
	ts := httptest.NewServer(s)
	defer ts.Close()

	bad := post(t, ts, "m", url.Values{"token": {"bad"}})
	if bad["error"] != "invalid_auth" {
		t.Fatalf("body = %v", bad)
	}
	good := post(t, ts, "m", url.Values{"token": {"good"}})
	if good["kept"] != true {
		t.Errorf("fixture consumed by rejected call: %v", good)
	}
	if calls := len(s.CallsFor("m")); calls != 2 {
		t.Errorf("recorded %d calls, want 2", calls)
	}
}
