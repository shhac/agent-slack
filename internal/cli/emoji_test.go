package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func emojiListFixture() map[string]any {
	return map[string]any{"ok": true, "emoji": map[string]any{
		"partyparrot": "https://emoji.slack-edge.com/T1/partyparrot/abc.gif",
		"shipit":      "alias:squirrel",
		"squirrel":    "https://emoji.slack-edge.com/T1/squirrel/def.png",
	}}
}

func TestEmojiListLeanByDefault(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("emoji.list", emojiListFixture())

	out, _, err := f.run(t, "emoji", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 3 {
		t.Fatalf("want 3 emoji, got %d: %v", len(lines), lines)
	}
	// Sorted by name; URLs omitted without --full.
	if lines[0]["name"] != "partyparrot" {
		t.Errorf("row 0 = %v", lines[0])
	}
	if _, present := lines[0]["url"]; present {
		t.Errorf("url should be omitted without --full, got %v", lines[0])
	}
	// Aliases still surface their target.
	if lines[1]["name"] != "shipit" || lines[1]["alias_for"] != "squirrel" {
		t.Errorf("row 1 = %v", lines[1])
	}
}

func TestEmojiListPaginationMeta(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("emoji.list", emojiListFixture()) // 3 emoji

	out, _, err := f.run(t, "emoji", "list", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	// 2 rows + a trailing @pagination meta line.
	if len(lines) != 3 {
		t.Fatalf("want 2 rows + pagination meta, got %d: %v", len(lines), lines)
	}
	pg, ok := lines[2]["@pagination"].(map[string]any)
	if !ok || pg["next_cursor"] == nil {
		t.Fatalf("pagination meta = %v", lines[2])
	}
	out2, _, err := f.run(t, "emoji", "list", "--limit", "2", "--cursor", pg["next_cursor"].(string))
	if err != nil {
		t.Fatal(err)
	}
	lines2 := parseNDJSON(t, out2)
	if len(lines2) != 1 {
		t.Fatalf("page 2 = %d lines, want 1 row no cursor: %v", len(lines2), lines2)
	}
}

func TestEmojiListFull(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("emoji.list", emojiListFixture())

	out, _, err := f.run(t, "emoji", "list", "--full")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if lines[0]["url"] == nil || lines[0]["url"] == "" {
		t.Errorf("--full should include url, got %v", lines[0])
	}
}

func TestEmojiGetCustom(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("emoji.list", emojiListFixture())

	out, _, err := f.run(t, "emoji", "get", ":partyparrot:")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["name"] != "partyparrot" || payload["custom"] != true {
		t.Errorf("payload = %v", payload)
	}
}

func TestEmojiGetStandardFallback(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("emoji.list", emojiListFixture())

	out, _, err := f.run(t, "emoji", "get", "rocket")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["custom"] != false || payload["unicode"] != "🚀" {
		t.Errorf("payload = %v", payload)
	}
}

func TestEmojiGetMultipleWithUnresolved(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("emoji.list", emojiListFixture())

	out, _, err := f.run(t, "emoji", "get", "partyparrot", "no_such_emoji_xyz")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	// One resolved row plus the trailing @unresolved meta line.
	if len(lines) != 2 {
		t.Fatalf("want 1 row + unresolved meta, got %d: %v", len(lines), lines)
	}
	unresolved, ok := lines[1]["@unresolved"].([]any)
	if !ok || len(unresolved) != 1 || unresolved[0] != "no_such_emoji_xyz" {
		t.Errorf("unresolved meta = %v", lines[1])
	}
}

func writeCLITempPNG(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "emoji.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nrest"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEmojiAddRequiresYes(t *testing.T) {
	f := newCLIFixture(t)
	img := writeCLITempPNG(t)

	_, stderr, err := f.run(t, "emoji", "add", "facepalm", "--image", img)
	if err == nil {
		t.Fatal("expected error without --yes")
	}
	if errPayload(t, stderr)["fixable_by"] != "human" {
		t.Errorf("payload = %v", errPayload(t, stderr))
	}
	// No API call should have happened.
	if n := len(f.server.CallsFor("emoji.add")); n != 0 {
		t.Errorf("emoji.add called %d times before confirmation", n)
	}

	f.server.HandleBody("emoji.add", map[string]any{"ok": true})
	out, _, err := f.run(t, "emoji", "add", "facepalm", "--image", img, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["added"] != "facepalm" {
		t.Errorf("out = %s", out)
	}
}

func TestEmojiAddRejectsBothModes(t *testing.T) {
	f := newCLIFixture(t)
	img := writeCLITempPNG(t)
	_, stderr, err := f.run(t, "emoji", "add", "x", "--image", img, "--alias-for", "y", "--yes")
	if err == nil {
		t.Fatal("expected error when both --image and --alias-for are given")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("payload = %v", errPayload(t, stderr))
	}
}

func TestEmojiRemoveRequiresYes(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "emoji", "remove", "facepalm")
	if err == nil {
		t.Fatal("expected error without --yes")
	}
	if errPayload(t, stderr)["fixable_by"] != "human" {
		t.Errorf("payload = %v", errPayload(t, stderr))
	}
	if n := len(f.server.CallsFor("emoji.remove")); n != 0 {
		t.Errorf("emoji.remove called %d times before confirmation", n)
	}

	f.server.HandleBody("emoji.remove", map[string]any{"ok": true})
	out, _, err := f.run(t, "emoji", "remove", "facepalm", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["removed"] != "facepalm" {
		t.Errorf("out = %s", out)
	}
}

func emojiSearchFixture() map[string]any {
	return map[string]any{"ok": true, "emoji": map[string]any{
		"parrot":       "https://e/parrot.gif",
		"party-parrot": "https://e/party-parrot.gif",
		"sad_parrot":   "https://e/sad_parrot.gif",
		"rocket":       "https://e/rocket.gif",
	}}
}

func TestEmojiSearchRanked(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("emoji.list", emojiSearchFixture())

	out, _, err := f.run(t, "emoji", "search", "parrot")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 3 {
		t.Fatalf("want 3 parrot matches, got %d: %v", len(lines), lines)
	}
	// Exact match ranks first and carries a score + tier; URL omitted (lean).
	if lines[0]["name"] != "parrot" || lines[0]["match"] != "exact" {
		t.Errorf("row 0 = %v, want exact parrot", lines[0])
	}
	if _, present := lines[0]["url"]; present {
		t.Errorf("url should be omitted without --full, got %v", lines[0])
	}
	if _, present := lines[0]["score"]; !present {
		t.Errorf("score should be present, got %v", lines[0])
	}
}

func TestEmojiSearchPaginationMeta(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("emoji.list", emojiSearchFixture())

	out, _, err := f.run(t, "emoji", "search", "parrot", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	// 2 rows + a trailing @pagination meta line carrying next_cursor.
	if len(lines) != 3 {
		t.Fatalf("want 2 rows + pagination meta, got %d: %v", len(lines), lines)
	}
	pg, ok := lines[2]["@pagination"].(map[string]any)
	if !ok || pg["has_more"] != true || pg["next_cursor"] == nil {
		t.Fatalf("pagination meta = %v", lines[2])
	}

	// Feed the cursor back: the next page has the remaining match and no cursor.
	out2, _, err := f.run(t, "emoji", "search", "parrot", "--limit", "2", "--cursor", pg["next_cursor"].(string))
	if err != nil {
		t.Fatal(err)
	}
	lines2 := parseNDJSON(t, out2)
	if len(lines2) != 1 {
		t.Fatalf("page 2 = %d lines, want 1 row and no further cursor: %v", len(lines2), lines2)
	}
	if _, present := lines2[0]["@pagination"]; present {
		t.Errorf("last page should not carry pagination meta, got %v", lines2[0])
	}
}
