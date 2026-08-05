package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
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
	f.server.HandleBody("conversations.replies", mockslack.History())
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

// Reproduces the live failure this behavior was added for: a question posted
// to a conversation, answered by threading on it. Before RepliesTo, the
// channel-target default dropped that reply and the await reported silence
// while the answer sat in the thread.
func TestMessageAwaitMatchesThreadReplyToTheAwaitedMessage(t *testing.T) {
	question := "1700000010.000100"
	reply := mockslack.WSThreadReply(mockslack.WSChannelID, mockslack.WSOtherUser,
		"answered in the thread", "1700000020.000200", question)
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello(), reply})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID,
		"--since", question, "--timeout", "2s")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["received"] != true {
		t.Fatalf("an in-thread answer to --since must match: %v", payload)
	}
	event, _ := payload["event"].(map[string]any)
	if event["thread_ts"] != question || event["content"] != "answered in the thread" {
		t.Errorf("event = %v", event)
	}
}

// RepliesTo is narrow: it admits answers to the awaited message, not every
// thread in the conversation.
func TestMessageAwaitStillExcludesOtherThreads(t *testing.T) {
	other := mockslack.WSThreadReply(mockslack.WSChannelID, mockslack.WSOtherUser,
		"unrelated thread chatter", "1700000020.000200", "1700000005.000100")
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello(), other})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID,
		"--since", "1700000010.000100", "--timeout", "400ms")
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["received"] != false {
		t.Fatalf("another thread's reply is not an answer: %v", payload)
	}
}

// Self-exclusion is wired through the credential's cache key, which is parsed
// by hand — a unit test on the filter would not catch that wiring breaking.
// In a self-DM every message is your own, so a regression here makes await
// look permanently silent.
func TestMessageAwaitExcludesYourOwnMessages(t *testing.T) {
	own := mockslack.WSMessage(mockslack.WSChannelID, fixtureUserID, "posted by me", "1700000015.000100")
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello(), own})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--timeout", "400ms")
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["received"] != false {
		t.Fatalf("your own message is not a reply to yourself: %v", payload)
	}

	stdout, _, err = f.run(t, "message", "await", mockslack.WSChannelID, "--include-self", "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["received"] != true {
		t.Fatalf("--include-self should admit it: %v", payload)
	}
}

// The contract that one parser serves both commands: an await's `event` object
// and a stream line for the same frame must be identical. Divergence here is
// invisible in each command's own tests.
func TestAwaitEventAndStreamLineAreTheSameRecord(t *testing.T) {
	frames := []map[string]any{
		mockslack.Hello(),
		mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "one record", "1700000015.000100"),
	}

	f := watchCLIFixture(t, frames)
	awaitOut, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	awaitEvent, _ := parseJSON(t, awaitOut)["event"].(map[string]any)

	f2 := watchCLIFixture(t, frames)
	streamOut, _, err := f2.run(t, "message", "stream", "--max-events", "1", "--duration", "5s")
	if err != nil {
		t.Fatal(err)
	}
	streamLine := parseNDJSON(t, streamOut)[0]

	if !reflect.DeepEqual(awaitEvent, streamLine) {
		t.Errorf("await and stream must emit the same record\n await:  %v\n stream: %v", awaitEvent, streamLine)
	}
}

// "reaction" is one word to the caller but two kinds on the wire. A regression
// that expanded it to adds-only would make a retraction invisible.
func TestParseEventKindsExpandsAliases(t *testing.T) {
	kinds, err := parseEventKinds([]string{"reaction", "message", "reaction"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[slack.EventKind]bool{
		slack.EventReactionAdded:   true,
		slack.EventReactionRemoved: true,
		slack.EventMessage:         true,
	}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v; want %d distinct (duplicates collapsed)", kinds, len(want))
	}
	for _, kind := range kinds {
		if !want[kind] {
			t.Errorf("unexpected kind %q", kind)
		}
	}
	if _, err := parseEventKinds([]string{"typing"}); err == nil {
		t.Error("an unknown kind must be rejected, not silently ignored")
	}
}

// --reaction names what counts as an answer. Without implying the reaction
// kinds it would filter nothing and return the next *message* instead — the
// command silently doing something other than what was asked.
func TestMessageAwaitReactionFlagImpliesReactionEvents(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{
		mockslack.Hello(),
		mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "a message, not the answer", "1700000015.000100"),
		mockslack.WSReactionAdded(mockslack.WSChannelID, mockslack.WSOtherUser, "eyes", "1700000010.000100", "1700000030.000100"),
	})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--reaction", "eyes", "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	event, _ := payload["event"].(map[string]any)
	if event["event"] != "reaction_added" || event["reaction"] != "eyes" {
		t.Fatalf("--reaction alone must wait for that reaction, got %v", payload)
	}
}

// The same normalizer serves `message react` and `--reaction`, so a unicode
// emoji matches the shortcode name Slack puts on the wire.
func TestMessageAwaitReactionAcceptsUnicodeEmoji(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{
		mockslack.Hello(),
		mockslack.WSReactionAdded(mockslack.WSChannelID, mockslack.WSOtherUser, "rocket", "1700000010.000100", "1700000030.000100"),
	})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--reaction", "🚀", "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["received"] != true {
		t.Fatalf("--reaction 🚀 should match a :rocket: reaction, got %v", payload)
	}
}

// A typo should be an immediate agent-fixable error, not a silent timeout.
func TestMessageAwaitRejectsUnusableReactionName(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello()})

	_, stderr, err := f.run(t, "message", "await", mockslack.WSChannelID, "--reaction", "not an emoji!", "--timeout", "1s")
	if err == nil {
		t.Fatal("an unusable emoji must error rather than wait")
	}
	if payload := errPayload(t, stderr); payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v", payload["fixable_by"])
	}
}

// --from must key on the id *shape*. "Bella" is a handle, not a bot id; before
// the shape test it was passed through unresolved, producing a filter that
// silently matched nothing for the whole timeout.
func TestMessageAwaitFromHandleIsResolvedNotPassedThrough(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello()})

	_, stderr, err := f.run(t, "message", "await", mockslack.WSChannelID,
		"--from", "Bella", "--timeout", "1s")
	if err == nil {
		t.Fatal("an unresolvable handle must error, not wait out the timeout")
	}
	if payload := errPayload(t, stderr); payload["error"] == nil {
		t.Errorf("payload = %v", payload)
	}
}

// A real bot id passes through — apps have no user record to resolve.
func TestMessageAwaitFromAcceptsBotID(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{
		mockslack.Hello(),
		mockslack.WSBotMessage(mockslack.WSChannelID, "Fabricated App", "deploy done", "1700000015.000100"),
	})

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID,
		"--from", mockslack.WSBotID, "--timeout", "5s")
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["received"] != true {
		t.Fatalf("a bot id should pass through and match its app's post: %v", payload)
	}
}

// Streaming commands cannot buffer into a JSON document. Handing back NDJSON
// to a caller who asked for JSON is worse than refusing: they parse the first
// line as the whole result.
func TestMessageStreamRejectsNonNDJSONFormat(t *testing.T) {
	f := watchCLIFixture(t, mockslack.DefaultEventScript())

	_, stderr, err := f.run(t, "message", "stream", "--format", "json", "--duration", "200ms")
	if err == nil {
		t.Fatal("--format json must be refused, not silently ignored")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v", payload["fixable_by"])
	}
	if msg, _ := payload["error"].(string); !strings.Contains(msg, "--format") {
		t.Errorf("error should name the flag: %v", payload["error"])
	}
}

// The typed projection embeds render.CompactMessage, so a stream line carries
// every field a `message list` line does. Hand-copying those keys is how
// forwarded_threads silently went missing from the output.
func TestStreamLineCarriesEveryCompactMessageField(t *testing.T) {
	compactFields := map[string]bool{}
	compactType := reflect.TypeOf(render.CompactMessage{})
	for i := range compactType.NumField() {
		tag, _, _ := strings.Cut(compactType.Field(i).Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			compactFields[tag] = true
		}
	}

	eventFields := map[string]bool{}
	eventType := reflect.TypeOf(compactEvent{})
	for i := range eventType.NumField() {
		field := eventType.Field(i)
		if field.Anonymous {
			for name := range compactFields {
				eventFields[name] = true
			}
			continue
		}
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		eventFields[tag] = true
	}

	for name := range compactFields {
		if !eventFields[name] {
			t.Errorf("a stream line drops %q, which `message list` emits", name)
		}
	}
	if !eventFields["forwarded_threads"] {
		t.Error("forwarded_threads specifically — the field the hand-built map lost")
	}
}

// --channel takes the same target vocabulary as every other command. Before it
// went through the shared kernel, a permalink fell into the default branch and
// was resolved as if it were a channel *name* — an API call that could only fail.
func TestStreamChannelsResolveAPermalinkWithoutALookup(t *testing.T) {
	f := newBrowserCLIFixture(t)
	cc := &clientContext{Client: nil, WorkspaceURL: "https://acme.slack.com"}
	_ = f

	target, err := render.ParseTarget("https://acme.slack.com/archives/C0FAKECHAN/p1700000010000100")
	if err != nil {
		t.Fatal(err)
	}
	if target.Kind != render.TargetURL {
		t.Fatalf("target kind = %v, want a permalink", target.Kind)
	}
	// A nil client is deliberate: resolving a permalink must not call the API.
	got, err := channelIDForTarget(t.Context(), cc, target)
	if err != nil {
		t.Fatal(err)
	}
	if got != "C0FAKECHAN" {
		t.Errorf("channel = %q, want the permalink's conversation", got)
	}
}
