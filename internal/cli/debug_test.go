package cli

import (
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// wsCaptureFixture is a browser-auth CLI fixture whose mock server also serves
// the fake event socket.
func wsCaptureFixture(t *testing.T, script mockslack.WSScript) *cliFixture {
	t.Helper()
	f := newBrowserCLIFixture(t)
	f.server.EnableWebSocket(script)
	f.server.HandleBody("client.getWebSocketURL", mockslack.GetWebSocketURL(f.url))
	return f
}

func TestDebugWSCaptureStreamsFramesAndSummary(t *testing.T) {
	f := wsCaptureFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	stdout, _, err := f.run(t, "debug", "ws-capture", "--duration", "5s")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, stdout)
	script := mockslack.DefaultEventScript()
	if len(lines) != len(script)+1 { // frames + the @summary line
		t.Fatalf("got %d lines, want %d", len(lines), len(script)+1)
	}
	if lines[0]["type"] != "hello" {
		t.Errorf("first line = %v", lines[0])
	}
	summary, ok := lines[len(lines)-1]["@summary"].(map[string]any)
	if !ok {
		t.Fatalf("last line is not a summary: %v", lines[len(lines)-1])
	}
	if summary["frames"] != float64(len(script)) {
		t.Errorf("summary frames = %v", summary["frames"])
	}
	if socketURL, _ := summary["socket_url"].(string); strings.Contains(socketURL, "xoxc-test") {
		t.Errorf("summary leaks the token: %q", socketURL)
	}
}

func TestDebugWSCaptureQuietPrintsOnlySummary(t *testing.T) {
	f := wsCaptureFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	stdout, _, err := f.run(t, "debug", "ws-capture", "--duration", "5s", "--quiet")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, stdout)
	if len(lines) != 1 || lines[0]["@summary"] == nil {
		t.Fatalf("want only a summary line, got %v", lines)
	}
}

func TestDebugWSCaptureRejectsStandardAuth(t *testing.T) {
	f := newCLIFixture(t)

	_, stderr, err := f.run(t, "debug", "ws-capture", "--duration", "1s")
	if err == nil {
		t.Fatal("want an error on standard auth")
	}
	payload := errPayload(t, stderr)
	if payload["fixable_by"] != "human" {
		t.Errorf("fixable_by = %v", payload["fixable_by"])
	}
	if msg, _ := payload["error"].(string); !strings.Contains(msg, "browser auth") {
		t.Errorf("error = %v", payload["error"])
	}
}

func TestDebugWSCaptureRejectsMalformedSendFrame(t *testing.T) {
	f := wsCaptureFixture(t, mockslack.WSScript{Frames: mockslack.DefaultEventScript()})

	_, stderr, err := f.run(t, "debug", "ws-capture", "--duration", "1s", "--send", "not-json")
	if err == nil {
		t.Fatal("want an error for a malformed --send frame")
	}
	if payload := errPayload(t, stderr); payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v", payload["fixable_by"])
	}
}

// The command is a development tool, not part of the agent-facing surface.
func TestDebugCommandIsHidden(t *testing.T) {
	root := newRootCmdWithDeps(rootDeps{version: "test"})
	for _, cmd := range root.Commands() {
		if cmd.Name() == "debug" {
			if !cmd.Hidden {
				t.Error("debug command should be hidden")
			}
			return
		}
	}
	t.Fatal("debug command not registered")
}
