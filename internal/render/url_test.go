package render

import "testing"

func TestParseMessageURL(t *testing.T) {
	ref, err := ParseMessageURL("https://stablygroup.slack.com/archives/C060RS20UMV/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	if ref.WorkspaceURL != "https://stablygroup.slack.com" {
		t.Errorf("WorkspaceURL = %q", ref.WorkspaceURL)
	}
	if ref.ChannelID != "C060RS20UMV" {
		t.Errorf("ChannelID = %q", ref.ChannelID)
	}
	if ref.MessageTS != "1770165109.628379" {
		t.Errorf("MessageTS = %q", ref.MessageTS)
	}
	if ref.ThreadTSHint != "" || ref.PossiblyTruncated {
		t.Errorf("unexpected thread hint %q / truncated %v", ref.ThreadTSHint, ref.PossiblyTruncated)
	}
}

func TestParseMessageURLThreadTS(t *testing.T) {
	ref, err := ParseMessageURL("https://stablygroup.slack.com/archives/C060RS20UMV/p1770165109628379?thread_ts=1770160000.000001&cid=C060RS20UMV")
	if err != nil {
		t.Fatal(err)
	}
	if ref.MessageTS != "1770165109.628379" {
		t.Errorf("MessageTS = %q", ref.MessageTS)
	}
	if ref.ThreadTSHint != "1770160000.000001" {
		t.Errorf("ThreadTSHint = %q", ref.ThreadTSHint)
	}
	if ref.PossiblyTruncated {
		t.Error("PossiblyTruncated should be false when cid is present")
	}
}

func TestParseMessageURLTruncationHint(t *testing.T) {
	// thread_ts without cid usually means the shell ate "&cid=…".
	ref, err := ParseMessageURL("https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379?thread_ts=1770160000.000001")
	if err != nil {
		t.Fatal(err)
	}
	if !ref.PossiblyTruncated {
		t.Error("expected PossiblyTruncated")
	}

	// A malformed thread_ts is dropped as a hint but still flags truncation.
	ref, err = ParseMessageURL("https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379?thread_ts=bogus")
	if err != nil {
		t.Fatal(err)
	}
	if ref.ThreadTSHint != "" {
		t.Errorf("ThreadTSHint = %q, want empty for malformed value", ref.ThreadTSHint)
	}
	if !ref.PossiblyTruncated {
		t.Error("expected PossiblyTruncated for thread_ts without cid")
	}
}

func TestParseMessageURLErrors(t *testing.T) {
	cases := map[string]string{
		"not a URL":         "general",
		"empty":             "",
		"non-slack host":    "https://example.com/archives/C1/p1770165109628379",
		"bare slack.com":    "https://slack.com/archives/C1/p1770165109628379",
		"wrong path":        "https://acme.slack.com/team/U123",
		"short path":        "https://acme.slack.com/archives/C1",
		"bad message id":    "https://acme.slack.com/archives/C1/x1770165109628379",
		"too few ts digits": "https://acme.slack.com/archives/C1/p123456",
	}
	for name, input := range cases {
		if _, err := ParseMessageURL(input); err == nil {
			t.Errorf("%s: expected error for %q", name, input)
		}
	}
}

func TestBuildMessageURLRoot(t *testing.T) {
	got := BuildMessageURL(MessageURLParts{
		WorkspaceURL: "https://stablygroup.slack.com/",
		ChannelID:    "C060RS20UMV",
		MessageTS:    "1770165109.628379",
	})
	want := "https://stablygroup.slack.com/archives/C060RS20UMV/p1770165109628379"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildMessageURLReply(t *testing.T) {
	got := BuildMessageURL(MessageURLParts{
		WorkspaceURL: "https://stablygroup.slack.com",
		ChannelID:    "C060RS20UMV",
		MessageTS:    "1770165110.000001",
		ThreadTS:     "1770165109.628379",
	})
	want := "https://stablygroup.slack.com/archives/C060RS20UMV/p1770165110000001?thread_ts=1770165109.628379&cid=C060RS20UMV"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildMessageURLRootThreadTSOmitted(t *testing.T) {
	// ThreadTS equal to MessageTS means "root message" — no thread params.
	got := BuildMessageURL(MessageURLParts{
		WorkspaceURL: "https://acme.slack.com",
		ChannelID:    "C1",
		MessageTS:    "1770165109.628379",
		ThreadTS:     "1770165109.628379",
	})
	if got != "https://acme.slack.com/archives/C1/p1770165109628379" {
		t.Errorf("got %q", got)
	}
}

func TestIsMessageTS(t *testing.T) {
	valid := []string{"1770165109.628379", "123456.000001"}
	invalid := []string{"", "1770165109", "1770165109.62837", "12345.123456", "abc.def", "1770165109.6283790"}
	for _, ts := range valid {
		if !IsMessageTS(ts) {
			t.Errorf("IsMessageTS(%q) = false, want true", ts)
		}
	}
	for _, ts := range invalid {
		if IsMessageTS(ts) {
			t.Errorf("IsMessageTS(%q) = true, want false", ts)
		}
	}
}
