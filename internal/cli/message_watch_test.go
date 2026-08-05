package cli

import (
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// watchCLIFixture is a browser-auth fixture whose mock server also serves the
// fake event socket. The socket is held open so the engine does not read a
// finished script as a dropped connection.
func watchCLIFixture(t *testing.T, frames []map[string]any) *cliFixture {
	t.Helper()
	f := newBrowserCLIFixture(t)
	f.server.EnableWebSocket(mockslack.WSScript{Frames: frames, KeepOpen: true})
	f.server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(f.url))
	f.server.HandleBody("conversations.history", mockslack.History())
	return f
}

func TestMessageAwaitReturnsTheNextMessage(t *testing.T) {
	f := watchCLIFixture(t, mockslack.DefaultEventScript())

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["received"] != true {
		t.Fatalf("payload = %v", payload)
	}
	event, ok := payload["event"].(map[string]any)
	if !ok {
		t.Fatalf("event missing: %v", payload)
	}
	if event["event"] != "message" || event["channel_id"] != mockslack.WSChannelID {
		t.Errorf("event = %v", event)
	}
	// The cursor is what a follow-up call passes as --since.
	if payload["cursor"] == "" || payload["cursor"] == nil {
		t.Error("a received event must come with a cursor")
	}
}

// A timeout is a successful outcome; exit 0 with a cursor to resume from.
func TestMessageAwaitTimeoutIsNotAnError(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello()})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID,
		"--since", "1700000010.000100", "--timeout", "200ms")
	if err != nil {
		t.Fatalf("timeout should not error: %v", err)
	}
	payload := parseJSON(t, stdout)
	if payload["received"] != false {
		t.Fatalf("payload = %v", payload)
	}
	if payload["cursor"] != "1700000010.000100" {
		t.Errorf("cursor = %v, want the input echoed back", payload["cursor"])
	}
}

// Awaiting a ✅ while someone reacts ❌ must not look like silence.
func TestMessageAwaitReportsSkippedRejection(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{
		mockslack.Hello(),
		mockslack.WSReactionAdded(mockslack.WSChannelID, mockslack.WSOtherUser, "x", "1700000010.000100", "1700000030.000100"),
	})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID,
		"--events", "reaction", "--reaction", "white_check_mark", "--timeout", "400ms")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["received"] != false {
		t.Fatalf("the ❌ must not satisfy the await: %v", payload)
	}
	skipped, _ := payload["skipped"].([]any)
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v", payload["skipped"])
	}
	if first, _ := skipped[0].(map[string]any); first["reaction"] != "x" {
		t.Errorf("skipped entry = %v", skipped[0])
	}
}

// A skin-toned reaction is still that reaction.
func TestMessageAwaitMatchesSkinTonedReaction(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{
		mockslack.Hello(),
		mockslack.WSReactionAdded(mockslack.WSChannelID, mockslack.WSOtherUser, "+1::skin-tone-5", "1700000010.000100", "1700000030.000100"),
	})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID,
		"--events", "reaction", "--reaction", "+1", "--timeout", "2s")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["received"] != true {
		t.Fatalf("payload = %v", payload)
	}
}

func TestMessageAwaitRejectsUnknownEventKind(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello()})

	_, stderr, err := f.run(t, "message", "await", mockslack.WSChannelID, "--events", "typing", "--timeout", "1s")
	if err == nil {
		t.Fatal("want an error for an unknown event kind")
	}
	if payload := errPayload(t, stderr); payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v", payload["fixable_by"])
	}
}

func TestMessageStreamEmitsNDJSONWithSummary(t *testing.T) {
	f := watchCLIFixture(t, mockslack.DefaultEventScript())

	stdout, _, err := f.run(t, "message", "stream",
		"--channel", mockslack.WSChannelID, "--duration", "400ms", "--events", "message,reaction")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, stdout)
	if len(lines) < 2 {
		t.Fatalf("want events plus a summary, got %v", lines)
	}
	summary, ok := lines[len(lines)-1]["@summary"].(map[string]any)
	if !ok {
		t.Fatalf("last line is not a summary: %v", lines[len(lines)-1])
	}
	// Per-channel cursors: gap-fill is per conversation, so one scalar is not
	// a valid resume point.
	cursors, ok := summary["cursors"].(map[string]any)
	if !ok || cursors[mockslack.WSChannelID] == nil {
		t.Errorf("summary cursors = %v", summary["cursors"])
	}
	for _, line := range lines[:len(lines)-1] {
		kind, _ := line["event"].(string)
		if kind != "message" && !strings.HasPrefix(kind, "reaction_") {
			t.Errorf("unexpected event kind %q in %v", kind, line)
		}
	}
}

// Bookkeeping frames outnumber real activity ~15:1 on a live socket; none of
// them may reach the caller.
func TestMessageStreamDropsBookkeepingFrames(t *testing.T) {
	f := watchCLIFixture(t, mockslack.DefaultEventScript())

	stdout, _, err := f.run(t, "message", "stream", "--duration", "400ms")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, stdout)
	for _, line := range lines {
		if line["@summary"] != nil {
			continue
		}
		if line["event"] != "message" {
			t.Errorf("default stream should carry messages only, got %v", line)
		}
	}
}

func TestMessageStreamRequiresBrowserAuth(t *testing.T) {
	f := newCLIFixture(t) // standard token

	_, stderr, err := f.run(t, "message", "stream", "--duration", "200ms")
	if err == nil {
		t.Fatal("stream must refuse to poll a whole workspace")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" {
		t.Errorf("fixable_by = %v", payload["fixable_by"])
	}
	if msg, _ := payload["error"].(string); !strings.Contains(msg, "browser auth") {
		t.Errorf("error = %v", payload["error"])
	}
}

// await degrades to polling on a standard token rather than refusing.
func TestMessageAwaitPollsOnStandardAuth(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", mockslack.History(
		mockslack.Message("1700000015.000100", mockslack.WSOtherUser, "posted while polling"),
	))

	stdout, stderr, err := f.run(t, "message", "await", "C0FAKEPOLL",
		"--since", "1700000010.000100", "--timeout", "3s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "polling") {
		t.Errorf("the caller should be told delivery is degraded: %s", stderr)
	}
	payload := parseJSON(t, stdout)
	if payload["received"] != true {
		t.Fatalf("payload = %v", payload)
	}
}
