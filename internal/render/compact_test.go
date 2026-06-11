package render

import (
	"reflect"
	"strings"
	"testing"
)

func makeMessage(files ...FileSummary) MessageSummary {
	return MessageSummary{
		ChannelID: "C123",
		TS:        "1234567890.000001",
		Text:      "hello",
		Files:     files,
	}
}

func TestToCompactMessageFileDownloaded(t *testing.T) {
	msg := makeMessage(FileSummary{ID: "F1", Name: "diagram.png", Mimetype: "image/png", URLPrivate: "https://example.com/f1"})
	compact := ToCompactMessage(msg, CompactOptions{
		DownloadedPaths: map[string]DownloadResult{"F1": {OK: true, Path: "/tmp/F1.png"}},
	})
	want := []CompactFile{{Name: "diagram.png", Mimetype: "image/png", Path: "/tmp/F1.png"}}
	if !reflect.DeepEqual(compact.Files, want) {
		t.Errorf("Files = %+v, want %+v", compact.Files, want)
	}
}

func TestToCompactMessageFileDownloadError(t *testing.T) {
	msg := makeMessage(FileSummary{ID: "F1", Name: "diagram.png", Mimetype: "image/png", URLPrivate: "https://example.com/f1"})
	compact := ToCompactMessage(msg, CompactOptions{
		DownloadedPaths: map[string]DownloadResult{
			"F1": {OK: false, Error: "Failed to download file (404)", Path: "/tmp/F1.download-error.txt"},
		},
	})
	want := []CompactFile{{
		Name:     "diagram.png",
		Mimetype: "image/png",
		Path:     "/tmp/F1.download-error.txt",
		Error:    "Failed to download file (404)",
	}}
	if !reflect.DeepEqual(compact.Files, want) {
		t.Errorf("Files = %+v, want %+v", compact.Files, want)
	}
}

func TestToCompactMessageFileWithoutDownloadEntryOmitted(t *testing.T) {
	msg := makeMessage(FileSummary{ID: "F1", Mimetype: "image/png", URLPrivate: "https://example.com/f1"})
	compact := ToCompactMessage(msg, CompactOptions{DownloadedPaths: map[string]DownloadResult{}})
	if compact.Files != nil {
		t.Errorf("Files = %+v, want nil", compact.Files)
	}
}

func TestToCompactMessageMixedDownloads(t *testing.T) {
	msg := makeMessage(
		FileSummary{ID: "F1", Name: "diagram.png", Mimetype: "image/png", URLPrivate: "https://example.com/f1"},
		FileSummary{ID: "F2", Mimetype: "text/plain", Mode: "snippet", URLPrivate: "https://example.com/f2"},
	)
	compact := ToCompactMessage(msg, CompactOptions{
		DownloadedPaths: map[string]DownloadResult{
			"F1": {OK: true, Path: "/tmp/F1.png"},
			"F2": {OK: false, Error: "Failed to download file (401)", Path: "/tmp/F2.download-error.txt"},
		},
	})
	if len(compact.Files) != 2 {
		t.Fatalf("Files = %+v, want 2 entries", compact.Files)
	}
	if compact.Files[0].Path != "/tmp/F1.png" || compact.Files[0].Error != "" {
		t.Errorf("file[0] = %+v", compact.Files[0])
	}
	if compact.Files[1].Mode != "snippet" || compact.Files[1].Error != "Failed to download file (401)" {
		t.Errorf("file[1] = %+v", compact.Files[1])
	}
}

func TestToCompactMessageFailedDownloadKeepsMetadata(t *testing.T) {
	msg := makeMessage(FileSummary{ID: "F1", Mimetype: "image/png", URLPrivate: "https://example.com/f1"})
	compact := ToCompactMessage(msg, CompactOptions{
		DownloadedPaths: map[string]DownloadResult{
			"F1": {OK: false, Error: "Failed to download file (404)", Path: "/tmp/F1.download-error.txt"},
		},
	})
	if len(compact.Files) != 1 || compact.Files[0].Mimetype != "image/png" {
		t.Errorf("Files = %+v", compact.Files)
	}
}

func TestToCompactMessageNameDoesNotFallBackToTitle(t *testing.T) {
	msg := makeMessage(FileSummary{ID: "F2", Title: "My Document", Mimetype: "text/plain"})
	compact := ToCompactMessage(msg, CompactOptions{
		DownloadedPaths: map[string]DownloadResult{"F2": {OK: true, Path: "/tmp/F2/doc.txt"}},
	})
	want := []CompactFile{{Mimetype: "text/plain", Path: "/tmp/F2/doc.txt"}}
	if !reflect.DeepEqual(compact.Files, want) {
		t.Errorf("Files = %+v, want %+v", compact.Files, want)
	}
}

func TestToCompactMessageReactionsOnlyWhenEnabled(t *testing.T) {
	msg := MessageSummary{
		ChannelID: "C1",
		TS:        "1.000001",
		Text:      "hi",
		Reactions: []any{map[string]any{
			"name":  "rocket",
			"users": []any{"U12345678", "U87654321"},
			"count": float64(2),
		}},
	}

	off := ToCompactMessage(msg, CompactOptions{IncludeReactions: false})
	if off.Reactions != nil {
		t.Errorf("Reactions = %+v, want nil when disabled", off.Reactions)
	}

	on := ToCompactMessage(msg, CompactOptions{IncludeReactions: true})
	if len(on.Reactions) != 1 {
		t.Fatalf("Reactions = %+v", on.Reactions)
	}
	r := on.Reactions[0]
	if r.Name != "rocket" || len(r.Users) != 2 {
		t.Errorf("reaction = %+v", r)
	}
	if r.Count != 0 {
		t.Errorf("Count = %d, want 0 (matches len(users))", r.Count)
	}
}

func TestCompactReactions(t *testing.T) {
	got := CompactReactions([]any{
		map[string]any{"name": " rocket ", "users": []any{"U12345678", "not-an-id", float64(5)}, "count": float64(3)},
		map[string]any{"name": ""},     // dropped: empty name
		"not-a-record",                 // dropped
		map[string]any{"name": "eyes"}, // kept: no users
	})
	want := []CompactReaction{
		{Name: "rocket", Users: []string{"U12345678"}, Count: 3},
		{Name: "eyes", Users: []string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestToCompactMessageForwardedThreads(t *testing.T) {
	msg := MessageSummary{
		ChannelID: "C1",
		TS:        "1.000001",
		Text:      "outer",
		Attachments: []any{map[string]any{
			"is_share":    true,
			"reply_count": float64(4),
			"from_url":    "https://example.slack.com/archives/C222/p333?thread_ts=1771564510.386389&cid=C222",
		}},
	}
	compact := ToCompactMessage(msg, CompactOptions{})
	want := []ForwardedThread{{
		URL:        "https://example.slack.com/archives/C222/p333?thread_ts=1771564510.386389&cid=C222",
		ThreadTS:   "1771564510.386389",
		ChannelID:  "C222",
		ReplyCount: 4,
	}}
	if !reflect.DeepEqual(compact.ForwardedThreads, want) {
		t.Errorf("got %+v, want %+v", compact.ForwardedThreads, want)
	}
}

func TestExtractForwardedThreads(t *testing.T) {
	dup := map[string]any{
		"from_url": "https://x.slack.com/archives/C1/p1?thread_ts=1771564510.386389",
	}
	got := ExtractForwardedThreads([]any{
		dup,
		dup, // same map → same key → deduplicated
		map[string]any{"from_url": "https://x.slack.com/archives/C1/p1"},               // no thread_ts
		map[string]any{"from_url": "https://x.slack.com/archives/C1/p1?thread_ts=abc"}, // malformed ts
		map[string]any{"from_url": "::not a url"},
		map[string]any{"text": "no from_url"},
	})
	want := []ForwardedThread{{
		URL:      "https://x.slack.com/archives/C1/p1?thread_ts=1771564510.386389",
		ThreadTS: "1771564510.386389",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestToCompactMessageThreadTSInference(t *testing.T) {
	// A root message with replies gets thread_ts = ts.
	root := ToCompactMessage(MessageSummary{ChannelID: "C1", TS: "1.000001", ReplyCount: 3, Text: "x"}, CompactOptions{})
	if root.ThreadTS != "1.000001" {
		t.Errorf("ThreadTS = %q, want ts", root.ThreadTS)
	}
	// No replies, no thread_ts.
	plain := ToCompactMessage(MessageSummary{ChannelID: "C1", TS: "1.000001", Text: "x"}, CompactOptions{})
	if plain.ThreadTS != "" {
		t.Errorf("ThreadTS = %q, want empty", plain.ThreadTS)
	}
}

func TestToCompactMessageAuthor(t *testing.T) {
	withUser := ToCompactMessage(MessageSummary{ChannelID: "C1", TS: "1.1", User: "U12345678"}, CompactOptions{})
	if withUser.Author == nil || withUser.Author.UserID != "U12345678" {
		t.Errorf("Author = %+v", withUser.Author)
	}
	withBot := ToCompactMessage(MessageSummary{ChannelID: "C1", TS: "1.1", BotID: "B123"}, CompactOptions{})
	if withBot.Author == nil || withBot.Author.BotID != "B123" {
		t.Errorf("Author = %+v", withBot.Author)
	}
	anon := ToCompactMessage(MessageSummary{ChannelID: "C1", TS: "1.1"}, CompactOptions{})
	if anon.Author != nil {
		t.Errorf("Author = %+v, want nil", anon.Author)
	}
}

func TestToCompactMessageTruncation(t *testing.T) {
	long := strings.Repeat("a", 50)
	msg := MessageSummary{ChannelID: "C1", TS: "1.1", Text: long}

	truncated := ToCompactMessage(msg, CompactOptions{MaxBodyChars: 10})
	if truncated.Content != strings.Repeat("a", 10)+"\n…" {
		t.Errorf("Content = %q", truncated.Content)
	}

	unlimited := ToCompactMessage(msg, CompactOptions{MaxBodyChars: -1})
	if unlimited.Content != long {
		t.Errorf("Content = %q, want full text", unlimited.Content)
	}

	// Zero means the 8000-char default, not truncate-to-zero.
	defaulted := ToCompactMessage(msg, CompactOptions{})
	if defaulted.Content != long {
		t.Errorf("Content = %q, want full text under default limit", defaulted.Content)
	}

	// Truncation counts runes, not bytes.
	emoji := MessageSummary{ChannelID: "C1", TS: "1.1", Text: strings.Repeat("🚀", 20)}
	cut := ToCompactMessage(emoji, CompactOptions{MaxBodyChars: 5})
	if cut.Content != strings.Repeat("🚀", 5)+"\n…" {
		t.Errorf("Content = %q", cut.Content)
	}
}

func TestToFileSummary(t *testing.T) {
	got := ToFileSummary(map[string]any{
		"id":                   "F1",
		"name":                 "a.png",
		"title":                "A",
		"mimetype":             "image/png",
		"filetype":             "png",
		"mode":                 "hosted",
		"permalink":            "https://x/p",
		"url_private":          "https://x/u",
		"url_private_download": "https://x/d",
		"size":                 float64(123),
	})
	want := &FileSummary{
		ID: "F1", Name: "a.png", Title: "A", Mimetype: "image/png", Filetype: "png",
		Mode: "hosted", Permalink: "https://x/p", URLPrivate: "https://x/u",
		URLPrivateDownload: "https://x/d", Size: 123,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	if ToFileSummary(map[string]any{"name": "no-id"}) != nil {
		t.Error("expected nil for file without id")
	}
	if ToFileSummary("not-a-record") != nil {
		t.Error("expected nil for non-record")
	}
}

func TestTruncateBody(t *testing.T) {
	cases := []struct {
		s    string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel\n…"},
		{"hello", -1, "hello"},
		{"🚀🚀🚀🚀", 2, "🚀🚀\n…"}, // runes, not bytes
		{"", 0, ""},
		{"x", 0, "\n…"},
	}
	for _, tc := range cases {
		if got := TruncateBody(tc.s, tc.max); got != tc.want {
			t.Errorf("TruncateBody(%q, %d) = %q, want %q", tc.s, tc.max, got, tc.want)
		}
	}
}
