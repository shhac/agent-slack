package cli

import "testing"

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
