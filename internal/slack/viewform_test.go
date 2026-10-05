package slack

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildViewStatePrefersLiveStateAndRequiresValues(t *testing.T) {
	view := map[string]any{
		"blocks": []any{
			map[string]any{"type": "input", "block_id": "a",
				"label":   map[string]any{"type": "plain_text", "text": "Title"},
				"element": map[string]any{"type": "plain_text_input", "action_id": "x", "initial_value": "old"}},
			map[string]any{"type": "input", "block_id": "b",
				"label":   map[string]any{"type": "plain_text", "text": "Owner"},
				"element": map[string]any{"type": "users_select", "action_id": "x"}},
		},
		"state": map[string]any{"values": map[string]any{
			"a": map[string]any{"x": map[string]any{"type": "plain_text_input", "value": "edited"}},
		}},
	}

	_, _, err := buildViewState(view, nil)
	if hint := agentHint(t, err); !strings.Contains(hint, "Owner") {
		t.Errorf("hint = %q, want the missing required field named", hint)
	}

	state, _, err := buildViewState(view, map[string]string{"owner": "U0000000009"})
	if err != nil {
		t.Fatal(err)
	}
	if got := state["a"].(map[string]any)["x"].(map[string]any)["value"]; got != "edited" {
		t.Errorf("title = %v, want the live state over initial_value", got)
	}
	if got := state["b"].(map[string]any)["x"].(map[string]any)["selected_user"]; got != "U0000000009" {
		t.Errorf("owner = %v", got)
	}
}

// An untouched pre-filled field goes back exactly as it was: the wrong
// state key would silently clear an app's existing data on an Edit form.
func TestInitialEntryResubmitsEachInitialValue(t *testing.T) {
	for _, pair := range initialEntries {
		element := map[string]any{"type": "some_input", pair.initialKey: "x"}
		want := map[string]any{"type": "some_input", pair.stateKey: "x"}
		if got := initialEntry(element); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: entry %v, want %v", pair.initialKey, got, want)
		}
	}
	rich := map[string]any{"type": "rich_text_input", "initial_value": map[string]any{"type": "rich_text"}}
	if got := initialEntry(rich); got["rich_text_value"] == nil {
		t.Errorf("rich text entry = %v, want rich_text_value", got)
	}
	if got := initialEntry(map[string]any{"type": "static_select"}); got != nil {
		t.Errorf("an empty element seeds %v, want nothing", got)
	}
}

// Live state can name an element with nothing chosen; that is an empty
// field, so the initial value applies and a required field still errors.
func TestBuildViewStateTreatsAnEmptyLiveValueAsEmpty(t *testing.T) {
	view := map[string]any{
		"blocks": []any{map[string]any{"type": "input", "block_id": "sev",
			"label":   map[string]any{"type": "plain_text", "text": "Severity"},
			"element": map[string]any{"type": "static_select", "action_id": "x"}}},
		"state": map[string]any{"values": map[string]any{
			"sev": map[string]any{"x": map[string]any{"type": "static_select", "selected_option": nil}},
		}},
	}
	_, _, err := buildViewState(view, nil)
	if hint := agentHint(t, err); !strings.Contains(hint, "Severity") {
		t.Errorf("hint = %q, want the empty required field named", hint)
	}
}

func TestEntryDisplay(t *testing.T) {
	option := func(label string) map[string]any {
		return map[string]any{"text": map[string]any{"type": "plain_text", "text": label}}
	}
	cases := map[string]map[string]any{
		"plain":       {"value": "plain"},
		"Minor":       {"selected_option": option("Minor")},
		"A, B":        {"selected_options": []any{option("A"), option("B")}},
		"U1, U2":      {"selected_users": []any{"U1", "U2"}},
		"2026-01-31":  {"selected_date": "2026-01-31"},
		"C0000000001": {"selected_channel": "C0000000001"},
	}
	for want, entry := range cases {
		if got := entryDisplay(entry); got != want {
			t.Errorf("entryDisplay(%v) = %q, want %q", entry, got, want)
		}
	}
}
