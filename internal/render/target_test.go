package render

import (
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

func TestParseTarget(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Target
	}{
		{"#channel", "#general", Target{Kind: TargetChannel, Channel: "#general"}},
		{"bare channel name", "general", Target{Kind: TargetChannel, Channel: "#general"}},
		{"channel ID", "C060RS20UMV", Target{Kind: TargetChannel, Channel: "C060RS20UMV"}},
		{"DM channel ID", "D060RS20UMV", Target{Kind: TargetChannel, Channel: "D060RS20UMV"}},
		{"user ID", "U12345ABCDE", Target{Kind: TargetUser, UserID: "U12345ABCDE"}},
		{"user ID with whitespace", "  U09GDJJKCCW  ", Target{Kind: TargetUser, UserID: "U09GDJJKCCW"}},
		{"short U-prefix is a channel name", "U1234", Target{Kind: TargetChannel, Channel: "#U1234"}},
		{"@handle is a user target", "@alice", Target{Kind: TargetUser, UserID: "@alice"}},
		{"@U… normalizes to the bare id", "@U12345ABCDE", Target{Kind: TargetUser, UserID: "U12345ABCDE"}},
	}
	for _, tc := range cases {
		got, err := ParseTarget(tc.input)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestParseTargetURL(t *testing.T) {
	got, err := ParseTarget("https://stablygroup.slack.com/archives/C060RS20UMV/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != TargetURL || got.Ref == nil {
		t.Fatalf("got %+v, want url target", got)
	}
	if got.Ref.ChannelID != "C060RS20UMV" || got.Ref.MessageTS != "1770165109.628379" {
		t.Errorf("ref = %+v", got.Ref)
	}
}

func TestParseTargetChannelURL(t *testing.T) {
	// A channel URL (no /p<ts> message segment) is a channel target that pins
	// its workspace — not a bare name with the URL stuffed into it.
	got, err := ParseTarget("https://acme.slack.com/archives/D0A1B2C3D4E")
	if err != nil {
		t.Fatal(err)
	}
	want := Target{
		Kind:         TargetChannel,
		Channel:      "D0A1B2C3D4E",
		WorkspaceURL: "https://acme.slack.com",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseChannelURL(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantWS      string
		wantChannel string
		wantOK      bool
	}{
		{"channel URL", "https://acme.slack.com/archives/C060RS20UMV", "https://acme.slack.com", "C060RS20UMV", true},
		{"DM URL host-cased", "https://Acme.Slack.com/archives/D0A1B2C3D4E", "https://acme.slack.com", "D0A1B2C3D4E", true},
		{"message permalink is not a channel URL", "https://acme.slack.com/archives/C060RS20UMV/p1770165109628379", "", "", false},
		{"non-slack host", "https://evil.example.com/archives/C060RS20UMV", "", "", false},
		{"wrong path root", "https://acme.slack.com/messages/C060RS20UMV", "", "", false},
		{"not a channel id", "https://acme.slack.com/archives/notanid", "", "", false},
		{"bare name", "general", "", "", false},
	}
	for _, tc := range cases {
		ws, ch, ok := ParseChannelURL(tc.input)
		if ok != tc.wantOK || ws != tc.wantWS || ch != tc.wantChannel {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", tc.name, ws, ch, ok, tc.wantWS, tc.wantChannel, tc.wantOK)
		}
	}
}

func TestParseTargetEmpty(t *testing.T) {
	_, err := ParseTarget("   ")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) || apiErr.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("expected agent-fixable APIError, got %v", err)
	}
}

func TestIsChannelIDIsUserID(t *testing.T) {
	if !IsChannelID("C060RS20UMV") || !IsChannelID("D060RS20UMV") || !IsChannelID("G060RS20UMV") {
		t.Error("expected C/D/G IDs to be channel IDs")
	}
	if IsChannelID("U060RS20UMV") || IsChannelID("C1234567") || IsChannelID("c060rs20umv") {
		t.Error("unexpected channel ID match")
	}
	if !IsUserID("U12345ABCDE") {
		t.Error("expected user ID match")
	}
	if IsUserID("U1234") || IsUserID("W12345ABCDE") {
		t.Error("unexpected user ID match")
	}
}
