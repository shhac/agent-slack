package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeCacheCategory writes a category file directly for one workspace, with
// explicit fetched_at timestamps so recency ordering is deterministic.
func writeCacheCategory[T any](t *testing.T, dir, workspaceURL, category string, entries map[string]cacheEntry[T]) {
	t.Helper()
	wsDir := filepath.Join(dir, hashWorkspaceURL(workspaceURL))
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cacheData[T]{Version: cacheFileVersion, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, category+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func completionValues(items []CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Value
	}
	return out
}

func TestReadTargetCompletions(t *testing.T) {
	dir := t.TempDir()
	ws := "https://acme.slack.com"

	writeCacheCategory(t, dir, ws, "channels", map[string]cacheEntry[CompactChannel]{
		"C0DEVS": {FetchedAt: 300, Value: CompactChannel{ID: "C0DEVS", Name: "devs", Topic: "engineering"}},
		"C0GEN":  {FetchedAt: 100, Value: CompactChannel{ID: "C0GEN", Name: "general"}},
		"D0DM":   {FetchedAt: 400, Value: CompactChannel{ID: "D0DM", IsIM: true, User: "U0X"}},
	})
	writeCacheCategory(t, dir, ws, "users", map[string]cacheEntry[CompactUser]{
		"U0ALICE": {FetchedAt: 200, Value: CompactUser{ID: "U0ALICE", DisplayName: "Alice"}},
	})

	// No prefix: most-recently-fetched first; the DM is excluded (no name).
	got := completionValues(ReadTargetCompletions(dir, ws, "", 10))
	want := []string{"#devs", "U0ALICE", "#general"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q (recency order)", i, got[i], want[i])
		}
	}

	// Prefix filters case-insensitively, on name or id, with or without '#'.
	if got := completionValues(ReadTargetCompletions(dir, ws, "dev", 10)); len(got) != 1 || got[0] != "#devs" {
		t.Errorf("prefix dev: got %v", got)
	}
	if got := completionValues(ReadTargetCompletions(dir, ws, "#GEN", 10)); len(got) != 1 || got[0] != "#general" {
		t.Errorf("prefix #GEN: got %v", got)
	}
	if got := completionValues(ReadTargetCompletions(dir, ws, "u0al", 10)); len(got) != 1 || got[0] != "U0ALICE" {
		t.Errorf("prefix u0al: got %v", got)
	}

	// Cap is honored.
	if got := ReadTargetCompletions(dir, ws, "", 1); len(got) != 1 || got[0].Value != "#devs" {
		t.Errorf("cap=1: got %v", got)
	}
}

func TestReadCompletionsSourceFiltering(t *testing.T) {
	dir := t.TempDir()
	ws := "https://acme.slack.com"
	writeCacheCategory(t, dir, ws, "channels", map[string]cacheEntry[CompactChannel]{
		"C0DEVS": {FetchedAt: 100, Value: CompactChannel{ID: "C0DEVS", Name: "devs"}},
	})
	writeCacheCategory(t, dir, ws, "users", map[string]cacheEntry[CompactUser]{
		"U0ALICE": {FetchedAt: 100, Value: CompactUser{ID: "U0ALICE", Name: "alice", RealName: "Alice Anderson"}},
	})
	writeCacheCategory(t, dir, ws, "workflow-triggers", map[string]cacheEntry[WorkflowPreview]{
		"Ft0PING": {FetchedAt: 100, Value: WorkflowPreview{TriggerID: "Ft0PING", Name: "Ping Monitor"}},
	})

	// Channels-only must not leak users or triggers.
	if got := completionValues(ReadCompletions(dir, ws, "", 10, CompleteChannels)); len(got) != 1 || got[0] != "#devs" {
		t.Errorf("channels-only: %v", got)
	}
	// Users-only: completes to @handle with "Real Name (id)" as the hint, and
	// matches by handle, id, or name prefix.
	users := ReadCompletions(dir, ws, "", 10, CompleteUsers)
	if len(users) != 1 || users[0].Value != "@alice" || users[0].Description != "Alice Anderson (U0ALICE)" {
		t.Errorf("users-only: %+v", users)
	}
	if got := completionValues(ReadCompletions(dir, ws, "@al", 10, CompleteUsers)); len(got) != 1 || got[0] != "@alice" {
		t.Errorf("@-prefix: %v", got)
	}
	if got := completionValues(ReadCompletions(dir, ws, "U0AL", 10, CompleteUsers)); len(got) != 1 || got[0] != "@alice" {
		t.Errorf("id-prefix should still surface the handle: %v", got)
	}
	// Triggers-only, with the workflow name as the description.
	triggers := ReadCompletions(dir, ws, "", 10, CompleteTriggers)
	if len(triggers) != 1 || triggers[0].Value != "Ft0PING" || triggers[0].Description != "Ping Monitor" {
		t.Errorf("triggers-only: %+v", triggers)
	}
	// Combined draws from every requested source.
	if got := completionValues(ReadCompletions(dir, ws, "", 10, CompleteChannels|CompleteUsers|CompleteTriggers)); len(got) != 3 {
		t.Errorf("combined: %v", got)
	}
}

func TestReadTargetCompletionsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	ws := "https://acme.slack.com"
	wsDir := filepath.Join(dir, hashWorkspaceURL(ws))
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "channels.json"), []byte("{{{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A corrupt category must not panic or pollute results — and other intact
	// categories still complete.
	writeCacheCategory(t, dir, ws, "users", map[string]cacheEntry[CompactUser]{
		"U0ALICE": {FetchedAt: 100, Value: CompactUser{ID: "U0ALICE", DisplayName: "Alice"}},
	})
	got := completionValues(ReadTargetCompletions(dir, ws, "", 10))
	if len(got) != 1 || got[0] != "U0ALICE" {
		t.Errorf("got %v, want just the intact category's entry", got)
	}
}

func TestReadTargetCompletionsColdCache(t *testing.T) {
	if got := ReadTargetCompletions(t.TempDir(), "https://acme.slack.com", "", 10); len(got) != 0 {
		t.Errorf("cold cache should yield nothing, got %v", got)
	}
	if got := ReadTargetCompletions("", "", "", 10); len(got) != 0 {
		t.Errorf("no cache dir should yield nothing, got %v", got)
	}
}
