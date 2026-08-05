package slack

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// captureFixture wires a browser client to a mockslack server serving both
// client.getWebSocketURL and the fake socket.
func captureFixture(t *testing.T, script mockslack.WSScript) *Client {
	t.Helper()
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	server.EnableWebSocket(script)
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	return browserClientFor(ts.URL)
}

func TestCaptureEventsEmitsFramesAndTallies(t *testing.T) {
	c := captureFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	var got []CaptureFrame
	summary, err := CaptureEvents(context.Background(), c, CaptureOptions{Duration: 5 * time.Second},
		func(f CaptureFrame) error {
			got = append(got, f)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	script := mockslack.DefaultEventScript()
	if len(got) != len(script) {
		t.Fatalf("emitted %d frames, want %d", len(got), len(script))
	}
	if got[0].Type != "hello" || got[0].Seq != 1 {
		t.Errorf("first frame = %+v", got[0])
	}
	if summary.Frames != len(script) || summary.StoppedBy != StoppedByClosed {
		t.Errorf("summary = %+v", summary)
	}
	// Edits and deletes are message subtypes; a tally that merged them into
	// "message" would hide the distinction the capture exists to surface.
	if summary.ByType["message/message_changed"] != 1 || summary.ByType["message/message_deleted"] != 1 {
		t.Errorf("by_type = %v", summary.ByType)
	}
	if summary.ByType["message"] != 3 {
		t.Errorf("plain message count = %d, want 3", summary.ByType["message"])
	}
}

func TestCaptureEventsTypeFilterStillCountsEverything(t *testing.T) {
	c := captureFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	var got []CaptureFrame
	summary, err := CaptureEvents(context.Background(), c,
		CaptureOptions{Duration: 5 * time.Second, Types: []string{"user_typing"}},
		func(f CaptureFrame) error {
			got = append(got, f)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range got {
		if frame.Type != "user_typing" {
			t.Fatalf("emitted a filtered-out type: %+v", frame)
		}
	}
	if len(got) != summary.ByType["user_typing"] || len(got) == 0 {
		t.Fatalf("emitted %d frames, want every user_typing (%d)", len(got), summary.ByType["user_typing"])
	}
	if summary.Emitted != len(got) || summary.Frames != len(mockslack.DefaultEventScript()) {
		t.Errorf("summary = %+v", summary)
	}
}

func TestCaptureEventsStopsAtMaxFrames(t *testing.T) {
	c := captureFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript(), KeepOpen: true})

	summary, err := CaptureEvents(context.Background(), c,
		CaptureOptions{Duration: 5 * time.Second, MaxFrames: 2},
		func(CaptureFrame) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if summary.Frames != 2 || summary.StoppedBy != StoppedByMaxFrames {
		t.Errorf("summary = %+v", summary)
	}
}

func TestCaptureEventsStopsAtDuration(t *testing.T) {
	// A socket that greets and then goes quiet: only the deadline ends it.
	c := captureFixture(t, mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}, KeepOpen: true})

	summary, err := CaptureEvents(context.Background(), c,
		CaptureOptions{Duration: 150 * time.Millisecond},
		func(CaptureFrame) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if summary.Frames != 1 || summary.StoppedBy != StoppedByDuration {
		t.Errorf("summary = %+v", summary)
	}
}

func TestCaptureEventsSendsProbeFrames(t *testing.T) {
	server := mockslack.New()
	ts := httptest.NewServer(server)
	defer ts.Close()
	server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}, KeepOpen: true})
	server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(ts.URL))
	c := browserClientFor(ts.URL)

	probe := map[string]any{"type": "ping", "id": float64(1)}
	summary, err := CaptureEvents(context.Background(), c, CaptureOptions{
		Duration:  5 * time.Second,
		MaxFrames: 2, // hello + the pong our probe triggers
		Send:      []map[string]any{probe},
	}, func(CaptureFrame) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if summary.ByType["pong"] != 1 {
		t.Errorf("by_type = %v, want a pong", summary.ByType)
	}
	conns := server.WSConnections()
	if len(conns) != 1 || len(conns[0].Sent) != 1 || conns[0].Sent[0]["type"] != "ping" {
		t.Fatalf("recorded writes = %+v", conns)
	}
	// The cookie rides the upgrade request, exactly as the workflow socket does.
	if !strings.Contains(conns[0].Cookie, "d=xoxd-secret") {
		t.Errorf("cookie = %q", conns[0].Cookie)
	}
}

// A capture writes raw frames to a terminal or a file, so a token appearing
// anywhere in a payload must not survive the trip.
func TestCaptureEventsRedactsTokensInFrames(t *testing.T) {
	leaky := map[string]any{
		"type":  "hello",
		"debug": map[string]any{"url": "wss://example.invalid/?token=xoxc-leaked-value"},
	}
	c := captureFixture(t, mockslack.WSScript{Frames: []map[string]any{leaky}})

	var got []CaptureFrame
	if _, err := CaptureEvents(context.Background(), c, CaptureOptions{Duration: 5 * time.Second},
		func(f CaptureFrame) error {
			got = append(got, f)
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("frames = %+v", got)
	}
	nested, _ := got[0].Frame["debug"].(map[string]any)
	if url, _ := nested["url"].(string); strings.Contains(url, "xoxc-leaked-value") {
		t.Fatalf("token survived redaction: %q", url)
	}
}

func TestSortedTallyOrdersByCountThenName(t *testing.T) {
	got := SortedTally(map[string]int{"message": 2, "user_typing": 9, "hello": 2})
	want := []string{"user_typing=9", "hello=2", "message=2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tally = %v, want %v", got, want)
		}
	}
}

// Slack offers a second gateway for exactly this case. Returning on the first
// dial failure turns one gateway's outage into a failed run.
