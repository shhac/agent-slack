package cli

import (
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// The --channel fallback scans conversations.history directly instead of the
// search API (which misses recent messages and needs search:read).
func TestSearchMessagesChannelFallback(t *testing.T) {
	f := newCLIFixture(t)
	f.server.Handle("conversations.history",
		mockslack.Response{Body: historyWith(
			simpleMessage("1770165112.000003", "U2", "deploy finished cleanly"),
			simpleMessage("1770165111.000002", "U1", "unrelated chatter"),
			simpleMessage("1770165110.000001", "U1", "DEPLOY failed on eu-west"),
		)},
		mockslack.Response{Body: historyWith()}, // `latest` is exclusive: page 2 is empty
	)

	out, _, err := f.run(t, "search", "messages", "deploy", "--channel", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 {
		t.Fatalf("lines = %v", lines)
	}
	// Case-insensitive content match, no search.messages call.
	for _, line := range lines {
		if !strings.Contains(strings.ToLower(line["content"].(string)), "deploy") {
			t.Errorf("non-matching hit: %v", line)
		}
	}
	if len(f.server.CallsFor("search.messages")) != 0 {
		t.Error("channel fallback must not call the search API")
	}
}

func TestSearchMessagesChannelFallbackUserFilter(t *testing.T) {
	f := newCLIFixture(t)
	// Real Slack's `latest` cursor is exclusive, so page 2 is empty; a sticky
	// single fixture would re-serve page 1 and double-count.
	f.server.Handle("conversations.history",
		mockslack.Response{Body: historyWith(
			simpleMessage("2.000002", "U22222222", "deploy two"),
			simpleMessage("1.000001", "U11111111", "deploy one"),
		)},
		mockslack.Response{Body: historyWith()},
	)

	out, _, err := f.run(t, "search", "messages", "deploy", "--channel", "C12345678", "--user", "U11111111")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 || lines[0]["content"] != "deploy one" {
		t.Errorf("lines = %v", lines)
	}
}

func TestSearchFilesDownloadsMatches(t *testing.T) {
	f := newCLIFixture(t)
	host := fileHost(t, "image/png", "PNG-DATA")
	f.server.HandleBody("search.files", map[string]any{
		"ok": true,
		"files": map[string]any{
			"matches": []any{
				map[string]any{
					"id": "F0SEARCH1", "title": "Arch diagram", "mimetype": "image/png",
					"url_private_download": host.URL + "/f1",
				},
				map[string]any{
					"id": "F0SEARCH2", "title": "notes", "mimetype": "text/plain", "mode": "snippet",
					"url_private_download": host.URL + "/f2",
				},
			},
			"paging": map[string]any{"pages": float64(1)},
		},
	})

	out, _, err := f.run(t, "search", "files", "diagram", "--content-type", "image")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 { // snippet filtered out by --content-type image
		t.Fatalf("lines = %v", lines)
	}
	file := lines[0]["file"].(map[string]any)
	if file["title"] != "Arch diagram" || !strings.HasSuffix(file["path"].(string), "F0SEARCH1.png") {
		t.Errorf("file = %v", file)
	}
}

func TestSearchMessagesDateWindowFallback(t *testing.T) {
	f := newCLIFixture(t)
	// Unix for 2026-06-10T12:00Z ≈ 1781092800 (inside the window below);
	// 1781400000 is past --before; 1780000000 is before --after.
	f.server.Handle("conversations.history", mockslack.Response{Body: map[string]any{
		"ok": true,
		"messages": []any{
			simpleMessage("1781400000.000003", "U1", "deploy too new"),
			simpleMessage("1781092800.000002", "U1", "deploy in window"),
			simpleMessage("1780000000.000001", "U1", "deploy too old"),
		},
	}})

	out, _, err := f.run(t, "search", "messages", "deploy", "--channel", "C12345678",
		"--after", "2026-06-09", "--before", "2026-06-11")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 || lines[0]["content"] != "deploy in window" {
		t.Errorf("lines = %v", lines)
	}
	// The scan stops at the --after boundary (newest-first): one page only.
	if calls := len(f.server.CallsFor("conversations.history")); calls != 1 {
		t.Errorf("history calls = %d, want 1 (early exit at --after)", calls)
	}
}
