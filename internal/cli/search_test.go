package cli

import (
	"strings"
	"testing"
)

func TestSearchMessages(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("search.messages", map[string]any{
		"ok": true,
		"messages": map[string]any{
			"matches": []any{map[string]any{
				"ts":        "1770165109.628379",
				"channel":   map[string]any{"id": "C12345678"},
				"permalink": "https://acme.slack.com/archives/C12345678/p1770165109628379",
			}},
			"paging": map[string]any{"pages": float64(1)},
		},
	})
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.628379", "U12345678", "found me"),
	))

	out, _, err := f.run(t, "search", "messages", "found")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	if lines[0]["content"] != "found me" || lines[0]["permalink"] == "" {
		t.Errorf("hit = %v", lines[0])
	}
	if _, has := lines[0]["thread_ts"]; has {
		t.Error("search hits drop thread_ts")
	}
	if q := f.server.CallsFor("search.messages")[0].Params.Get("query"); q != "found" {
		t.Errorf("query = %q", q)
	}
}

func TestSearchQueryBuilding(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("search.messages", map[string]any{
		"ok":       true,
		"messages": map[string]any{"matches": []any{}},
	})

	_, _, err := f.run(t, "search", "messages", "deploy", "--after", "2026-06-01", "--before", "2026-06-10", "--user", "@paul")
	if err != nil {
		t.Fatal(err)
	}
	q := f.server.CallsFor("search.messages")[0].Params.Get("query")
	for _, want := range []string{"deploy", "after:2026-06-01", "before:2026-06-10", "from:@paul"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q missing %q", q, want)
		}
	}
}

func TestSearchInvalidDate(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "search", "messages", "x", "--after", "June 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("stderr = %s", stderr)
	}
}
