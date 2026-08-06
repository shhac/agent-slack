package cli

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--timeout", "1500ms")
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
	// Without this the agent cannot tell a clean timeout from a lost socket.
	if payload["stopped_by"] != "duration" {
		t.Errorf("stopped_by = %v, want the reason to reach the JSON", payload["stopped_by"])
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
	events := 0
	for _, line := range lines {
		if line["@summary"] != nil {
			continue
		}
		events++
		if line["event"] != "message" {
			t.Errorf("default stream should carry messages only, got %v", line)
		}
	}
	// The summary line always exists, so without this the test passes when no
	// event was delivered at all.
	if events == 0 {
		t.Fatal("the script's messages should have reached the stream")
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

	stdout, _, err = f.run(t, "message", "await", mockslack.WSChannelID, "--include-self", "--timeout", "1500ms")
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
	awaitOut, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--timeout", "1500ms")
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

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--reaction", "eyes", "--timeout", "1500ms")
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

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID, "--reaction", "🚀", "--timeout", "1500ms")
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
		"--from", mockslack.WSBotID, "--timeout", "1500ms")
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
			// Requiring the exact type is the point: any embedded struct would
			// otherwise satisfy this, including a hand-rolled shell that had
			// quietly dropped a field.
			if field.Type != compactType {
				t.Fatalf("embedded %s, want render.CompactMessage — the embed is what keeps the shapes in step", field.Type)
			}
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
	cc := &clientContext{WorkspaceURL: "https://acme.slack.com"}

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

// The help says a stream run is always bounded. Turning every bound off would
// otherwise produce a process that never returns to the agent that spawned it.
func TestMessageStreamRequiresABound(t *testing.T) {
	f := watchCLIFixture(t, mockslack.DefaultEventScript())

	_, stderr, err := f.run(t, "message", "stream",
		"--duration", "0", "--max-events", "0", "--idle-timeout", "0")
	if err == nil {
		t.Fatal("an unbounded stream must be refused")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v", payload["fixable_by"])
	}
}

// --poll is the only way to watch a conversation the socket stays silent on —
// your own DM publishes no socket events at all, for anyone, by any send path.
func TestMessageAwaitPollForcesHistoryOnBrowserAuth(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello()})
	// The socket delivers nothing, so only repeated history reads can find
	// this. The fixture's first (empty) response is the tip read; the message
	// appears two polls later, which also proves the loop keeps going.
	f.server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History()},
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000020.000200", mockslack.WSOtherUser, "found by polling"),
		)},
	)

	stdout, _, err := f.run(t, "message", "await", mockslack.WSChannelID,
		"--poll", "--poll-interval", "10ms", "--timeout", "1500ms")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["received"] != true {
		t.Fatalf("--poll should have found the message over history: %v", payload)
	}
	event, _ := payload["event"].(map[string]any)
	if event["content"] != "found by polling" {
		t.Errorf("event = %v", event)
	}
}

// Polling reads one conversation per interval; a workspace-wide poll would be
// a request storm.
func TestMessageStreamPollNeedsExactlyOneChannel(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello()})

	_, stderr, err := f.run(t, "message", "stream", "--poll", "--duration", "300ms")
	if err == nil {
		t.Fatal("a workspace-wide poll must be refused")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v", payload["fixable_by"])
	}
	// The hint must identify the CLI's up-front guard: the engine refuses the
	// same thing much later, and asserting only fixable_by cannot tell them
	// apart — so deleting the guard would leave this green.
	if hint, _ := payload["hint"].(string); !strings.Contains(hint, "--channel") {
		t.Errorf("hint = %q, want the up-front --channel guard", hint)
	}
}

// Every message in your own DM is yours, so the default self-exclusion would
// drop all of them and the documented command would report silence forever.
func TestMessageAwaitIncludesYourOwnMessagesInYourOwnDM(t *testing.T) {
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello()})
	f.server.HandleBody("conversations.open", map[string]any{
		"ok": true, "channel": map[string]any{"id": "D0FAKESELF1"},
	})
	f.server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History()},
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000020.000200", fixtureUserID, "a note to myself"),
		)},
	)

	stdout, _, err := f.run(t, "message", "await", "D0FAKESELF1",
		"--poll", "--poll-interval", "10ms", "--timeout", "1500ms")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, stdout)
	if payload["received"] != true {
		t.Fatalf("your own DM must not exclude your own messages: %v", payload)
	}
}

// Anywhere else the default stands: your own message is not a reply to you.
func TestMessageAwaitStillExcludesSelfInOtherConversations(t *testing.T) {
	// A DM with someone ELSE, so the identity comparison in isOwnDM is what
	// decides: a prefix-only check would wrongly include your own messages in
	// every DM you have.
	const otherDM = "D0FAKEOTHER1"
	f := watchCLIFixture(t, []map[string]any{
		mockslack.Hello(),
		mockslack.WSMessage(otherDM, fixtureUserID, "posted by me", "1700000015.000100"),
	})
	f.server.HandleBody("conversations.open", map[string]any{
		"ok": true, "channel": map[string]any{"id": "D0FAKESELF1"},
	})

	stdout, _, err := f.run(t, "message", "await", otherDM, "--timeout", "400ms")
	if err != nil {
		t.Fatal(err)
	}
	if payload := parseJSON(t, stdout); payload["received"] != false {
		t.Fatalf("self-exclusion should still apply outside your own DM: %v", payload)
	}
}

// The documented happy path for --poll on stream: one conversation, on a token
// that has no socket at all. Only the refusal path was covered, so wiring
// --poll to nothing would have gone unnoticed.
func TestMessageStreamPollDeliversOverHistory(t *testing.T) {
	f := newCLIFixture(t) // standard token: no event socket exists
	f.resolvableChannel("C0FAKEPOLLED")
	f.server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000010.000100", "U0FAKESAM", "the tip"),
		)},
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000020.000200", "U0FAKESAM", "arrived while polling"),
		)},
	)

	stdout, _, err := f.run(t, "message", "stream", "--poll", "--channel", "C0FAKEPOLLED",
		"--poll-interval", "10ms", "--max-events", "1", "--duration", "3s")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, stdout)
	if len(lines) < 2 {
		t.Fatalf("want an event plus the summary, got %v", lines)
	}
	if lines[0]["content"] != "arrived while polling" {
		t.Errorf("first line = %v", lines[0])
	}
}

// The reflection test above checks the type; this checks the wire. A hand-
// rolled projection that dropped forwarded_threads passed the type test, so
// the value has to be asserted in the emitted JSON too.
func TestStreamLineEmitsForwardedThreads(t *testing.T) {
	forwarded := mockslack.WSMessage(mockslack.WSChannelID, mockslack.WSOtherUser, "look at this", "1700000015.000100")
	forwarded["attachments"] = []any{map[string]any{
		"is_share":   true,
		"from_url":   "https://acme.slack.com/archives/C0FAKEOTHER/p1700000001000100?thread_ts=1700000001.000100&cid=C0FAKEOTHER",
		"text":       "the forwarded body",
		"ts":         "1700000001.000100",
		"channel_id": "C0FAKEOTHER",
	}}
	f := watchCLIFixture(t, []map[string]any{mockslack.Hello(), forwarded})

	stdout, _, err := f.run(t, "message", "stream", "--channel", mockslack.WSChannelID,
		"--max-events", "1", "--duration", "3s")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, stdout)
	if len(lines) == 0 {
		t.Fatal("no event emitted")
	}
	if lines[0]["forwarded_threads"] == nil {
		t.Errorf("stream line dropped forwarded_threads, which `message list` emits: %v", lines[0])
	}
}

// --poll's help names your own DM as the reason it exists. The exception that
// makes that work lived only in await, so stream emitted nothing for exactly
// the case it advertised — and advanced its cursor past the missed messages.
func TestMessageStreamIncludesYourOwnMessagesInYourOwnDM(t *testing.T) {
	f := newBrowserCLIFixture(t)
	f.server.EnableWebSocket(mockslack.WSScript{Frames: []map[string]any{mockslack.Hello()}, KeepOpen: true})
	f.server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(f.url))
	f.server.HandleBody("conversations.open", map[string]any{
		"ok": true, "channel": map[string]any{"id": "D0FAKESELF1"},
	})
	f.server.Handle("conversations.history",
		mockslack.Response{Body: mockslack.History()},
		mockslack.Response{Body: mockslack.History(
			mockslack.Message("1700000020.000200", fixtureUserID, "a note to myself"),
		)},
	)

	stdout, _, err := f.run(t, "message", "stream", "--poll", "--channel", "D0FAKESELF1",
		"--poll-interval", "10ms", "--max-events", "1", "--duration", "3s")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, stdout)
	if len(lines) < 2 || lines[0]["content"] != "a note to myself" {
		t.Fatalf("stream must not exclude your own messages in your own DM: %v", lines)
	}
}

// message stream resolves several --channel targets against ONE client, so a
// permalink from another workspace would resolve to a channel id that does not
// exist there — a run that watches nothing and reports no error.
func TestChannelTargetRejectsAnotherWorkspacesPermalink(t *testing.T) {
	cc := &clientContext{WorkspaceURL: "https://acme.slack.com"}
	target, err := render.ParseTarget("https://othercorp.slack.com/archives/C0OTHERCHAN/p1700000010000100")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := channelIDForTarget(t.Context(), cc, target); err == nil {
		t.Error("a cross-workspace permalink must be refused, not silently resolved")
	}

	// Same workspace still resolves without a lookup.
	same, err := render.ParseTarget("https://acme.slack.com/archives/C0FAKECHAN/p1700000010000100")
	if err != nil {
		t.Fatal(err)
	}
	got, err := channelIDForTarget(t.Context(), cc, same)
	if err != nil || got != "C0FAKECHAN" {
		t.Errorf("same-workspace permalink = %q, err = %v", got, err)
	}
}

// Flag help is the agent-facing contract for these commands, and hand-written
// copies drift: later's --ts once said "channel ID" while a #name target was
// rejected just the same, and five of eight --cursor strings omitted where the
// value comes from. Registering through one helper is what stops that, so the
// absence of hand-rolled copies is worth asserting.
func TestSharedFlagsAreRegisteredThroughTheirHelpers(t *testing.T) {
	root := newRootCmdWithDeps(rootDeps{version: "test"})

	seen := map[string]map[string]bool{"ts": {}, "cursor": {}, "slack-markdown": {}}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		for name := range seen {
			if f := cmd.Flags().Lookup(name); f != nil {
				seen[name][f.Usage] = true
			}
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(root)

	// --ts on `channel mark` means something genuinely different ("mark read up
	// to"), so it is allowed its own wording; everything else shares one.
	if len(seen["ts"]) > 2 {
		t.Errorf("--ts has %d distinct help strings: %v", len(seen["ts"]), keysOf(seen["ts"]))
	}
	if len(seen["cursor"]) != 1 {
		t.Errorf("--cursor has %d distinct help strings, want 1: %v", len(seen["cursor"]), keysOf(seen["cursor"]))
	}
	// Read-side and write-side wordings are deliberately distinct.
	if len(seen["slack-markdown"]) != 2 {
		t.Errorf("--slack-markdown has %d distinct help strings, want 2: %v",
			len(seen["slack-markdown"]), keysOf(seen["slack-markdown"]))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
