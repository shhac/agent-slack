package slack

import (
	"bytes"
	"strings"
	"testing"
)

// The debug log is the one place secrets could leak into a transcript, so the
// redaction is pinned directly: every xox* token family, in params and in
// response bodies (including nested), must come out as [redacted].

func debugClient() (*Client, *bytes.Buffer) {
	var buf bytes.Buffer
	c := New(Auth{Type: AuthStandard, Token: "xoxb-secret"}, WithDebug(&buf))
	return c, &buf
}

func TestDebugParamsRedactsSecrets(t *testing.T) {
	c, buf := debugClient()
	c.debugParams("chat.postMessage", map[string]string{
		"token":       "xoxc-super-secret-123",
		"xoxd_cookie": "some-cookie-value",
		"cookie":      "d=xoxd-abc",
		"channel":     "C0123ABCD",
		"text":        "hello world",
		"weird":       "xoxp-inline-token-shape", // any xox-prefixed VALUE redacts too
	})
	out := buf.String()

	for _, secret := range []string{"xoxc-super-secret-123", "some-cookie-value", "d=xoxd-abc", "xoxp-inline"} {
		if strings.Contains(out, secret) {
			t.Errorf("debug params leaked %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "channel=C0123ABCD") || !strings.Contains(out, "text=hello world") {
		t.Errorf("non-secret params should log in clear:\n%s", out)
	}
	if got := strings.Count(out, "[redacted]"); got != 4 {
		t.Errorf("redacted %d values, want 4:\n%s", got, out)
	}
}

func TestDebugParamsTruncatesLongValues(t *testing.T) {
	c, buf := debugClient()
	c.debugParams("chat.postMessage", map[string]string{"blocks": strings.Repeat("a", 500)})
	out := buf.String()
	if strings.Contains(out, strings.Repeat("a", 201)) {
		t.Errorf("long value not truncated:\n%.120s…", out)
	}
	if !strings.Contains(out, "…") {
		t.Error("truncation must be marked")
	}
}

func TestDebugResponseRedactsEmbeddedTokens(t *testing.T) {
	c, buf := debugClient()
	c.debugResponse("auth.test", map[string]any{
		"ok":    true,
		"token": "xoxb-top-level-1234567890",
		"nested": map[string]any{
			"deep": []any{map[string]any{"access_token": "xoxp-deep-9876543210"}},
		},
		"text": "user pasted xoxd-AbC%2F123-token in a message",
		"team": "Acme",
	})
	out := buf.String()

	for _, family := range []string{"xoxb-top-level", "xoxp-deep", "xoxd-AbC"} {
		if strings.Contains(out, family) {
			t.Errorf("debug response leaked %q:\n%s", family, out)
		}
	}
	if !strings.Contains(out, "[redacted]") || !strings.Contains(out, "Acme") {
		t.Errorf("expected redactions plus clear non-secrets:\n%s", out)
	}
}

func TestDebugJSONFrameRedactsEmbeddedTokens(t *testing.T) {
	c, buf := debugClient()
	// RTM frames carry arbitrary workspace push payloads — same redaction
	// contract as API responses.
	c.debugJSON("RTM frame", map[string]any{
		"type": "message",
		"text": "someone pasted xoxc-frame-secret-42 here",
		"view": map[string]any{"private_metadata": "xoxd-frame-deep-77"},
	})
	out := buf.String()
	for _, family := range []string{"xoxc-frame-secret", "xoxd-frame-deep"} {
		if strings.Contains(out, family) {
			t.Errorf("debug frame leaked %q:\n%s", family, out)
		}
	}
	if !strings.Contains(out, "RTM frame") || !strings.Contains(out, "[redacted]") {
		t.Errorf("expected labeled, redacted frame line:\n%s", out)
	}
}

func TestDebugResponseTruncatesLongBodies(t *testing.T) {
	c, buf := debugClient()
	c.debugResponse("conversations.history", map[string]any{"blob": strings.Repeat("x", 3*debugBodyLimit)})
	out := buf.String()
	if !strings.Contains(out, "…(truncated)") {
		t.Error("oversized body must be marked truncated")
	}
	// One line of method prefix + capped body; generous upper bound.
	if len(out) > debugBodyLimit+200 {
		t.Errorf("debug line length %d exceeds the cap", len(out))
	}
}

func TestDebugSoftFailureFlagging(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
		want string
	}{
		{"rejected triggers", map[string]any{"rejected_triggers": []any{map[string]any{"id": "Ft1"}}}, "soft-failure=rejected_triggers"},
		{"warning", map[string]any{"warning": "something_minor"}, "soft-failure=warning"},
		{"empty rejected list is fine", map[string]any{"rejected_triggers": []any{}}, ""},
		{"clean response", map[string]any{"ok": true}, ""},
	}
	for _, tc := range cases {
		got := debugSoftFailure(tc.data)
		if tc.want == "" && got != "" {
			t.Errorf("%s: got %q, want no flag", tc.name, got)
		}
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDebugSilentWhenDisabled(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "xoxb-x"}) // no WithDebug
	// Must be no-ops, not panics.
	c.debugParams("m", map[string]string{"k": "v"})
	c.debugResponse("m", map[string]any{"ok": true})
	c.debugJSON("RTM frame", map[string]any{"ok": true})
	c.debugf("hello")
}
