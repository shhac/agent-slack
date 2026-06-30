package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCacheCategory writes a category file directly for one workspace, with
// explicit fetched_at timestamps so recency ordering is deterministic.
func writeCacheCategory[T any](t *testing.T, dir, key, category string, entries map[string]cacheEntry[T]) {
	t.Helper()
	wsDir := filepath.Join(dir, key)
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
	ws := testKey

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

	// Prefix matches the value-form: bare "dev" → "devs", "#" prefix → "#general",
	// id prefix → the id (each entity is offered in all three forms).
	if got := completionValues(ReadTargetCompletions(dir, ws, "dev", 10)); len(got) != 1 || got[0] != "devs" {
		t.Errorf("prefix dev: got %v", got)
	}
	if got := completionValues(ReadTargetCompletions(dir, ws, "#dev", 10)); len(got) != 1 || got[0] != "#devs" {
		t.Errorf("prefix #dev: got %v", got)
	}
	if got := completionValues(ReadTargetCompletions(dir, ws, "#GEN", 10)); len(got) != 1 || got[0] != "#general" {
		t.Errorf("prefix #GEN: got %v", got)
	}
	if got := completionValues(ReadTargetCompletions(dir, ws, "C0DE", 10)); len(got) != 1 || got[0] != "C0DEVS" {
		t.Errorf("id prefix: got %v", got)
	}
	if got := completionValues(ReadTargetCompletions(dir, ws, "u0al", 10)); len(got) != 1 || got[0] != "U0ALICE" {
		t.Errorf("prefix u0al: got %v", got)
	}

	// Bare tab (no input) offers only the primary form per entity, capped.
	if got := ReadTargetCompletions(dir, ws, "", 1); len(got) != 1 || got[0].Value != "#devs" {
		t.Errorf("cap=1: got %v", got)
	}
}

func TestReadCompletionsSourceFiltering(t *testing.T) {
	dir := t.TempDir()
	ws := testKey
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
	// Users-only: each form's hint shows the OTHER two datapoints — a
	// handle/name form names the id, the id form names the handle.
	users := ReadCompletions(dir, ws, "", 10, CompleteUsers)
	if len(users) != 1 || users[0].Value != "@alice" || users[0].Description != "Alice Anderson (U0ALICE)" {
		t.Errorf("primary @handle: %+v", users)
	}
	if got := ReadCompletions(dir, ws, "@al", 10, CompleteUsers); len(got) != 1 || got[0].Value != "@alice" || got[0].Description != "Alice Anderson (U0ALICE)" {
		t.Errorf("@-prefix: %+v", got)
	}
	if got := ReadCompletions(dir, ws, "al", 10, CompleteUsers); len(got) != 1 || got[0].Value != "alice" || got[0].Description != "Alice Anderson (U0ALICE)" {
		t.Errorf("bare handle: %+v", got)
	}
	if got := ReadCompletions(dir, ws, "U0AL", 10, CompleteUsers); len(got) != 1 || got[0].Value != "U0ALICE" || got[0].Description != "Alice Anderson (@alice)" {
		t.Errorf("id form must name the handle: %+v", got)
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

// A bulk warm stamps every entry with the same fetched_at. Ordering — and,
// once the cap truncates, which entries survive — must be deterministic
// (alphabetical), not the randomized map-iteration order.
func TestReadCompletionsEqualFetchedIsStableAndAlphabetical(t *testing.T) {
	dir := t.TempDir()
	ws := testKey
	users := map[string]cacheEntry[CompactUser]{}
	for _, name := range []string{"zoe", "alex", "mary", "bob", "yara"} {
		users["U0"+strings.ToUpper(name)] = cacheEntry[CompactUser]{
			FetchedAt: 100, // identical timestamp — the tie the bug hinged on
			Value:     CompactUser{ID: "U0" + strings.ToUpper(name), Name: name},
		}
	}
	writeCacheCategory(t, dir, ws, "users", users)

	// Same result every call (no map-iteration shuffle), alphabetical by value.
	want := []string{"@alex", "@bob", "@mary", "@yara", "@zoe"}
	for range 5 {
		got := completionValues(ReadCompletions(dir, ws, "", 50, CompleteUsers))
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("position %d: got %q, want %q (alphabetical tiebreak)", i, got[i], want[i])
			}
		}
	}

	// The cap keeps the alphabetically-first N — a stable subset, not a random one.
	capped := completionValues(ReadCompletions(dir, ws, "", 2, CompleteUsers))
	if len(capped) != 2 || capped[0] != "@alex" || capped[1] != "@bob" {
		t.Errorf("capped subset must be the alphabetical head: %v", capped)
	}
}

func TestReadTargetCompletionsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	ws := testKey
	wsDir := filepath.Join(dir, ws)
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

// R16: addUsergroups was uncovered — seed the usergroup-entities cache and assert
// the three completion forms (@handle primary, bare handle, id) plus the
// id-only fallback for a handle-less group.
func TestReadCompletionsUsergroups(t *testing.T) {
	dir := t.TempDir()
	ws := testKey
	writeCacheCategory(t, dir, ws, "usergroup-entities", map[string]cacheEntry[CompactUsergroup]{
		"S0MKT": {FetchedAt: 100, Value: CompactUsergroup{ID: "S0MKT", Handle: "marketing", Name: "Marketing"}},
		"S0NOH": {FetchedAt: 100, Value: CompactUsergroup{ID: "S0NOH", Name: "No Handle Group"}},
	})

	if got := ReadCompletions(dir, ws, "@mark", 10, CompleteUsergroups); len(got) != 1 || got[0].Value != "@marketing" {
		t.Errorf("@-prefix usergroup: %+v", got)
	}
	if got := ReadCompletions(dir, ws, "mark", 10, CompleteUsergroups); len(got) != 1 || got[0].Value != "marketing" {
		t.Errorf("bare handle: %+v", got)
	}
	if got := ReadCompletions(dir, ws, "S0MKT", 10, CompleteUsergroups); len(got) != 1 || got[0].Value != "S0MKT" {
		t.Errorf("id form: %+v", got)
	}
	// A handle-less group is offered by id only.
	if got := ReadCompletions(dir, ws, "S0NOH", 10, CompleteUsergroups); len(got) != 1 || got[0].Value != "S0NOH" {
		t.Errorf("handle-less group id-only: %+v", got)
	}
}
