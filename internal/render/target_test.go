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
		{"channel ID", "C0123ABCD", Target{Kind: TargetChannel, Channel: "C0123ABCD"}},
		{"DM channel ID", "D0123ABCD", Target{Kind: TargetChannel, Channel: "D0123ABCD"}},
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
	got, err := ParseTarget("https://acme.slack.com/archives/C0123ABCD/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != TargetURL || got.Ref == nil {
		t.Fatalf("got %+v, want url target", got)
	}
	if got.Ref.ChannelID != "C0123ABCD" || got.Ref.MessageTS != "1770165109.628379" {
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
		{"channel URL", "https://acme.slack.com/archives/C0123ABCD", "https://acme.slack.com", "C0123ABCD", true},
		{"DM URL host-cased", "https://Acme.Slack.com/archives/D0A1B2C3D4E", "https://acme.slack.com", "D0A1B2C3D4E", true},
		{"message permalink is not a channel URL", "https://acme.slack.com/archives/C0123ABCD/p1770165109628379", "", "", false},
		{"non-slack host", "https://evil.example.com/archives/C0123ABCD", "", "", false},
		{"wrong path root", "https://acme.slack.com/messages/C0123ABCD", "", "", false},
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
	if !IsChannelID("C0123ABCD") || !IsChannelID("D0123ABCD") || !IsChannelID("G0123ABCD") {
		t.Error("expected C/D/G IDs to be channel IDs")
	}
	if IsChannelID("U0123ABCD") || IsChannelID("C1234567") || IsChannelID("c060rs20umv") {
		t.Error("unexpected channel ID match")
	}
	if !IsUserID("U12345ABCDE") {
		t.Error("expected user ID match")
	}
	// W-prefixed ids belong to Enterprise Grid and Slack Connect users. This
	// deliberately changed: rejecting them did not fail loudly — a W-prefixed
	// target fell through to channel-name resolution, and a W-prefixed reactor
	// was dropped from output — while mention rendering accepted them all
	// along, so one user was two different things depending on the code path.
	if !IsUserID("W12345ABCDE") {
		t.Error("expected an Enterprise Grid / Connect user ID to be a user ID")
	}
	if IsUserID("U1234") || IsUserID("W1234") {
		t.Error("unexpected user ID match: too short")
	}
	if IsUserID("Wendy") || IsUserID("WORKFLOW") {
		t.Error("a handle is not an id: the shape rule (9+ uppercase alphanumerics) is what separates them")
	}
	// One rule for one concept: target parsing and mention rendering must not
	// disagree about what a user id is.
	for _, id := range []string{"U12345ABCDE", "W12345ABCDE", "Wendy", "U1234"} {
		if IsUserID(id) != IsReferencedUserID(id) {
			t.Errorf("IsUserID(%q)=%v but IsReferencedUserID=%v", id, IsUserID(id), IsReferencedUserID(id))
		}
	}
}

// The shape test exists because a bare prefix check swallows handles: --from
// "Bella" would be treated as a bot id and never match anything.
func TestIsBotIDRequiresTheIDShape(t *testing.T) {
	if !IsBotID("B0FAKEAPP01") {
		t.Error("a real bot id should be recognised")
	}
	for _, notABot := range []string{"Bella", "B", "bot", "B0fakeapp01", "U0FAKEUSER1"} {
		if IsBotID(notABot) {
			t.Errorf("IsBotID(%q) should be false", notABot)
		}
	}
}

// The three places the U-only rule failed silently, pinned end to end.
func TestEnterpriseUserIDsWorkAsTargetsAndReactors(t *testing.T) {
	const enterpriseUser = "W01ENTERPRISE"

	// 1. As a target it must be a user, not a channel name. Before, it fell
	//    through to channel resolution and looked up "#W01ENTERPRISE".
	target, err := ParseTarget(enterpriseUser)
	if err != nil {
		t.Fatal(err)
	}
	if target.Kind != TargetUser || target.UserID != enterpriseUser {
		t.Errorf("target = %+v, want a user target", target)
	}

	// 2. As a reactor it must survive into output. Before, it was filtered out
	//    of reactions[].users and only the count hinted anything was missing.
	msg := MessageSummary{
		ChannelID: "C0123ABCD",
		TS:        "1700000010.000100",
		Reactions: []any{map[string]any{
			"name":  "eyes",
			"users": []any{"U12345ABCDE", enterpriseUser},
			"count": float64(2),
		}},
	}
	compact := ToCompactMessage(msg, CompactOptions{IncludeReactions: true})
	if len(compact.Reactions) != 1 {
		t.Fatalf("reactions = %+v", compact.Reactions)
	}
	if len(compact.Reactions[0].Users) != 2 {
		t.Errorf("reactors = %v, want the Enterprise Grid user kept", compact.Reactions[0].Users)
	}

	// 3. Inside a mention it resolves, as it always did.
	if !IsReferencedUserID(enterpriseUser) {
		t.Error("mention rendering should still accept it")
	}
}

// Every predicate that answers "is this an X id" must agree about a real id.
// They drifted once — IsUserID rejected W… while IsReferencedUserID accepted
// it — and the symptom was silent misrouting rather than an error, so the
// agreement is worth pinning rather than trusting.
func TestIDPredicatesAgreePerConcept(t *testing.T) {
	users := map[string]bool{
		"U12345ABCDE": true, "W12345ABCDE": true, // both id forms
		"Wendy": false, "U1234": false, "C12345ABCDE": false,
	}
	for id, want := range users {
		if IsUserID(id) != want || IsReferencedUserID(id) != want {
			t.Errorf("user %q: IsUserID=%v IsReferencedUserID=%v, want %v",
				id, IsUserID(id), IsReferencedUserID(id), want)
		}
	}

	groups := map[string]bool{"S12345ABCDE": true, "U12345ABCDE": false, "Subteam": false}
	for id, want := range groups {
		if IsUsergroupID(id) != want || IsReferencedUsergroupID(id) != want {
			t.Errorf("usergroup %q: IsUsergroupID=%v IsReferencedUsergroupID=%v, want %v",
				id, IsUsergroupID(id), IsReferencedUsergroupID(id), want)
		}
	}

	// Channels are the one concept where the two rules differ on purpose: a
	// DM is a conversation but cannot be mentioned, so it is a channel id and
	// not a referenced one. Asserted so the difference stays deliberate.
	if !IsChannelID("D12345ABCDE") {
		t.Error("a DM is a channel id")
	}
	if IsReferencedChannelID("D12345ABCDE") {
		t.Error("a DM cannot appear in a <#…> mention")
	}
	for _, id := range []string{"C12345ABCDE", "G12345ABCDE"} {
		if !IsChannelID(id) || !IsReferencedChannelID(id) {
			t.Errorf("%q should be both a channel id and a referenced channel id", id)
		}
	}
}
