package cli

import (
	"net/url"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// handleUser registers a users.info answer scoped to one user id, so a fixture
// can resolve several distinct users (the sticky single-body handler would
// return the same user for every id).
func (f *cliFixture) handleUser(id, name string) {
	f.server.HandleWhen("users.info", func(p url.Values) bool {
		return p.Get("user") == id
	}, mockslack.Response{Body: mockslack.UserInfo(id, name)})
}

// TestMessageGetTranscript renders a single message as natural-language text
// (not JSON) when --format transcript is opted into. The display zone is pinned
// to UTC for a deterministic stamp.
func TestMessageGetTranscript(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1782032540.314239", "U12345555", "Hello <@U87654321>")))
	f.handleUser("U12345555", "alice")
	f.handleUser("U87654321", "bob")

	out, _, err := f.run(t, "message", "get",
		"https://acme.slack.com/archives/C0123ABCD/p1782032540314239",
		"--format", "transcript", "--tz", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("transcript should be plain text, got JSON:\n%s", out)
	}
	if !strings.Contains(out, "[2026-06-21 @ 09:02:20 (UTC)] <alice|U12345555>") {
		t.Errorf("missing/wrong header line:\n%s", out)
	}
	if !strings.Contains(out, "  Hello @bob") {
		t.Errorf("mention should resolve to @bob in indented body:\n%s", out)
	}
}

// TestMessageGetTranscriptWithIDs toggles the ts id region on the header.
func TestMessageGetTranscriptWithIDs(t *testing.T) {
	run := func(args ...string) string {
		f := newCLIFixture(t)
		f.server.HandleBody("conversations.history", historyWith(
			simpleMessage("1782032540.314239", "U12345555", "hi")))
		full := append([]string{"message", "get",
			"https://acme.slack.com/archives/C0123ABCD/p1782032540314239",
			"--format", "transcript", "--tz", "UTC"}, args...)
		out, _, err := f.run(t, full...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := run(); strings.Contains(out, "⟨ts") {
		t.Errorf("default (no --with-ids) should be clean:\n%s", out)
	}
	if out := run("--with-ids"); !strings.Contains(out, "⟨ts 1782032540.314239⟩") {
		t.Errorf("--with-ids should append the verbatim ts:\n%s", out)
	}
}

// TestMessageListTranscript renders channel history as a transcript run.
func TestMessageListTranscript(t *testing.T) {
	f := newCLIFixture(t)
	f.resolvableChannel("C0123ABCD")
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1782032540.000000", "U12345555", "first"),
		simpleMessage("1782032600.000000", "U12345555", "second"),
	))

	out, _, err := f.run(t, "message", "list", "#general",
		"--format", "transcript", "--tz", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  first") || !strings.Contains(out, "  second") {
		t.Errorf("both messages should render:\n%s", out)
	}
	// A blank line separates messages.
	if !strings.Contains(out, "first\n\n[") {
		t.Errorf("messages should be blank-line separated:\n%s", out)
	}
}

// TestTranscriptUnknownTimezone errors with a structured agent-fixable message
// on stderr (text transcript only on success; errors stay JSON).
func TestTranscriptUnknownTimezone(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1782032540.314239", "U12345555", "hi")))

	_, stderr, err := f.run(t, "message", "get",
		"https://acme.slack.com/archives/C0123ABCD/p1782032540314239",
		"--format", "transcript", "--tz", "Mars/Phobos")
	if err == nil {
		t.Fatal("expected an error for an unknown timezone")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v, want agent", payload["fixable_by"])
	}
	if s, _ := payload["error"].(string); !strings.Contains(s, "Mars/Phobos") {
		t.Errorf("error should name the bad zone, got %q", s)
	}
}

// TestTranscriptRejectedOnNonConversationCommand confirms a command that did
// NOT opt in rejects --format transcript with a structured error.
func TestTranscriptRejectedOnNonConversationCommand(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "channel", "get", "C0123ABCD", "--format", "transcript")
	if err == nil {
		t.Fatal("expected --format transcript to be rejected on `channel get`")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v, want agent", payload["fixable_by"])
	}
	if s, _ := payload["error"].(string); !strings.Contains(s, "transcript") {
		t.Errorf("error should mention the rejected format, got %q", s)
	}
}
