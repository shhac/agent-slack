package slack

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestEncodeParam(t *testing.T) {
	if _, ok := encodeParam(nil); ok {
		t.Error("nil params must be dropped")
	}
	cases := map[string]any{
		"s":            "s",
		"true":         true,
		"42":           42,
		"43":           int64(43),
		"1.5":          1.5,
		"3":            float64(3), // no trailing .0
		`{"k":"v"}`:    map[string]any{"k": "v"},
		`["a"]`:        []any{"a"},
		`{"limit":25}`: map[string]int{"limit": 25},
	}
	for want, in := range cases {
		got, ok := encodeParam(in)
		if !ok || got != want {
			t.Errorf("encodeParam(%#v) = (%q,%v), want %q", in, got, ok, want)
		}
	}
	if _, ok := encodeParam(func() {}); ok {
		t.Error("unmarshalable values must be dropped, not panic")
	}
}

func TestEncodeMultipartDeterministic(t *testing.T) {
	fields := map[string]string{"b": "2", "a": "1", "c": "3"}
	first, _, err := encodeMultipart(fields)
	if err != nil {
		t.Fatal(err)
	}
	// Boundary is random per writer, but field ORDER must be stable: a, b, c.
	body := string(first)
	a, b, c := strings.Index(body, `name="a"`), strings.Index(body, `name="b"`), strings.Index(body, `name="c"`)
	if a > b || b > c {
		t.Errorf("fields not sorted:\n%s", body)
	}
}

func fakeHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestParseResponseEdges(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "xoxb-x"})

	// Unparseable 200 body collapses to an empty object → ok:false → error,
	// never a panic or a nil-map success.
	if _, _, err := c.parseResponse("m", fakeHTTPResponse(200, "<html>not json</html>")); err == nil {
		t.Error("malformed 200 body must error (ok is absent)")
	}

	// 429 reports a retry delay from Retry-After (clamped semantics live in call()).
	resp := fakeHTTPResponse(429, "")
	resp.Header.Set("Retry-After", "7")
	_, retryAfter, err := c.parseResponse("m", resp)
	if retryAfter.Seconds() != 7 || err == nil {
		t.Errorf("429: retryAfter=%v err=%v", retryAfter, err)
	}
	// Garbage Retry-After falls back to 5s.
	resp = fakeHTTPResponse(429, "")
	resp.Header.Set("Retry-After", "soon")
	if _, retryAfter, _ = c.parseResponse("m", resp); retryAfter.Seconds() != 5 {
		t.Errorf("garbage Retry-After: %v", retryAfter)
	}

	// ok:true passes the data through.
	data, _, err := c.parseResponse("m", fakeHTTPResponse(200, `{"ok":true,"x":1}`))
	if err != nil || data["x"].(float64) != 1 {
		t.Errorf("ok:true: data=%v err=%v", data, err)
	}
}

func TestChatResultMapping(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("chat.postMessage", map[string]any{
		"ok": true, "channel": "D0ECHOED12", "ts": "1781279107.445159",
	})
	c := newStandardClient(t, server)
	res, err := PostMessage(t.Context(), c, OutgoingMessage{ChannelID: "U0INPUT", Text: "hi"})
	if err != nil || res.ChannelID != "D0ECHOED12" || res.TS != "1781279107.445159" {
		t.Errorf("post result = %+v, err %v (channel must prefer Slack's echo)", res, err)
	}

	// Missing echo falls back to the input channel; missing ts stays empty.
	server2 := mockslack.New()
	server2.HandleBody("chat.postMessage", map[string]any{"ok": true})
	server2.HandleBody("chat.scheduleMessage", map[string]any{
		"ok": true, "scheduled_message_id": "Q1", "post_at": float64(1800000600),
	})
	c2 := newStandardClient(t, server2)
	res, err = PostMessage(t.Context(), c2, OutgoingMessage{ChannelID: "C0INPUT", Text: "hi"})
	if err != nil || res.ChannelID != "C0INPUT" || res.TS != "" {
		t.Errorf("fallback post result = %+v, err %v", res, err)
	}
	sched, err := ScheduleMessage(t.Context(), c2, OutgoingMessage{ChannelID: "C0INPUT", Text: "hi"}, 1800000000)
	if err != nil || sched.PostAt != 1800000600 || sched.ScheduledMessageID != "Q1" {
		t.Errorf("schedule result = %+v, err %v (post_at must prefer Slack's rounded echo)", sched, err)
	}
}
