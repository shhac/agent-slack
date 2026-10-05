package slack

import (
	"encoding/json"
	"strings"
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

func button(actionID, text string) map[string]any {
	return map[string]any{"type": "button", "action_id": actionID,
		"text": map[string]any{"type": "plain_text", "text": text}}
}

func cardWith(blocks ...any) map[string]any {
	return map[string]any{"ts": "1700000000.000100", "bot_id": "B0000000001", "blocks": blocks}
}

func actionsBlock(blockID string, elements ...any) map[string]any {
	return map[string]any{"type": "actions", "block_id": blockID, "elements": elements}
}

func agentHint(t *testing.T, err error) string {
	t.Helper()
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) || apiErr.FixableBy != agenterrors.FixableByAgent {
		t.Fatalf("err = %v, want an agent-fixable error", err)
	}
	return apiErr.Hint
}

func TestSelectMessageAction(t *testing.T) {
	card := cardWith(
		actionsBlock("first", button("approve", "Approve"), button("reject", "Reject")),
		actionsBlock("second", button("approve", "Approve")),
	)

	got, err := SelectMessageAction(card, ActionSelector{Label: "reject"})
	if err != nil || got.BlockID != "first" || got.Element["action_id"] != "reject" {
		t.Errorf("label match = %+v, %v", got, err)
	}

	_, err = SelectMessageAction(card, ActionSelector{Label: "Approve"})
	if hint := agentHint(t, err); !strings.Contains(hint, "--block-id") || !strings.Contains(hint, "second/approve") {
		t.Errorf("ambiguous hint = %q", hint)
	}

	got, err = SelectMessageAction(card, ActionSelector{ActionID: "approve", BlockID: "second"})
	if err != nil || got.BlockID != "second" {
		t.Errorf("--action-id + --block-id = %+v, %v", got, err)
	}
}

func TestSelectMessageActionRefusesLinkButtons(t *testing.T) {
	link := button("logs", "Logs")
	link["url"] = "https://ci.example.invalid/run/42"
	_, err := SelectMessageAction(cardWith(actionsBlock("b", link)), ActionSelector{Label: "Logs"})
	if hint := agentHint(t, err); !strings.Contains(hint, "https://ci.example.invalid/run/42") {
		t.Errorf("hint = %q, want the link to fetch instead", hint)
	}
}

func TestSelectMessageActionNamesLegacyAttachmentButtons(t *testing.T) {
	msg := map[string]any{"bot_id": "B0000000001", "attachments": []any{
		map[string]any{"actions": []any{map[string]any{"type": "button", "name": "ack"}}},
	}}
	_, err := SelectMessageAction(msg, ActionSelector{Label: "ack"})
	if hint := agentHint(t, err); !strings.Contains(hint, "legacy attachment") {
		t.Errorf("hint = %q", hint)
	}
}

func TestBlockActionParamsAddressTheApp(t *testing.T) {
	ref := &render.MessageRef{ChannelID: "C0000000001", MessageTS: "1700000000.000100"}
	target := render.InteractiveElement{BlockID: "b", Element: button("go", "Go")}

	// A message posted through an app with a user token carries app_id but
	// no bot_id; it is still the app that handles the press.
	params, err := blockActionParams(PressInput{Ref: ref, Target: target,
		Message: map[string]any{"app_id": "A0000000001", "team": "T0000000001"}})
	if err != nil {
		t.Fatal(err)
	}
	if params["service_id"] != "A0000000001" || params["service_team_id"] != "T0000000001" {
		t.Errorf("params = %v", params)
	}
	var actions []map[string]any
	if err := json.Unmarshal([]byte(params["actions"].(string)), &actions); err != nil || actions[0]["block_id"] != "b" {
		t.Errorf("actions = %v (%v)", params["actions"], err)
	}

	_, err = blockActionParams(PressInput{Ref: ref, Target: target, Message: map[string]any{"user": "U0000000001"}})
	agentHint(t, err)
}

func TestPressRelatedOnlyKeepsThisMessageAndApp(t *testing.T) {
	in := PressInput{
		Ref:     &render.MessageRef{ChannelID: "C1", MessageTS: "1.1"},
		Message: map[string]any{"app_id": "A1"},
	}
	cases := []struct {
		name  string
		frame map[string]any
		want  bool
	}{
		{"this app's view", map[string]any{"type": "view_opened", "view": map[string]any{"app_id": "A1"}}, true},
		{"stub view", map[string]any{"type": "view_opened", "view": map[string]any{"id": "V1"}}, true},
		{"another app's view", map[string]any{"type": "view_opened", "view": map[string]any{"app_id": "A2"}}, false},
		{"edit of the card", map[string]any{"type": "message", "subtype": "message_changed", "channel": "C1", "message": map[string]any{"ts": "1.1"}}, true},
		{"edit of another message", map[string]any{"type": "message", "subtype": "message_changed", "channel": "C1", "message": map[string]any{"ts": "2.2"}}, false},
		{"delete of the card", map[string]any{"type": "message", "subtype": "message_deleted", "channel": "C1", "deleted_ts": "1.1"}, true},
		{"new message", map[string]any{"type": "message", "channel": "C1", "ts": "3.3"}, false},
	}
	for _, tc := range cases {
		if got := pressRelated(tc.frame, in); got != tc.want {
			t.Errorf("%s: pressRelated = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMessageChangedIgnoresReactions(t *testing.T) {
	before := map[string]any{"text": "a", "blocks": []any{"x"}}
	if messageChanged(before, map[string]any{"text": "a", "blocks": []any{"x"}, "reactions": []any{"y"}}) {
		t.Error("a reaction is not the app's response")
	}
	if !messageChanged(before, map[string]any{"text": "a", "blocks": []any{"z"}}) {
		t.Error("changed blocks should count")
	}
}

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

func TestActionChoiceRefusesAValueForAButton(t *testing.T) {
	_, err := ActionChoice(render.InteractiveElement{BlockID: "b", Element: button("go", "Go")}, "x")
	agentHint(t, err)
}

// The outcome decides whether an agent may press again, so every
// combination of what was seen maps to exactly one, and a blind spot is
// always named in a warning.
func TestPressWatchResult(t *testing.T) {
	view := map[string]any{"id": "V1"}
	msg := map[string]any{"ts": "1.1"}
	cases := []struct {
		name         string
		watch        pressWatch
		want         string
		wantWarnings int
	}{
		{"nothing changed", pressWatch{readOK: true}, OutcomeNone, 0},
		{"card updated", pressWatch{readOK: true, updated: msg}, OutcomeMessageUpdated, 0},
		{"card deleted", pressWatch{readOK: true, deleted: true}, OutcomeMessageDeleted, 0},
		{"form beats an update", pressWatch{readOK: true, updated: msg, view: view}, OutcomeViewOpened, 0},
		{"never re-read", pressWatch{}, OutcomeUnknown, 1},
		{"socket lost, card readable", pressWatch{readOK: true, socketLost: true}, OutcomeNone, 1},
		{"socket lost and never re-read", pressWatch{socketLost: true}, OutcomeUnknown, 2},
		{"socket lost after the form", pressWatch{socketLost: true, readOK: true, view: view}, OutcomeViewOpened, 0},
	}
	for _, tc := range cases {
		got := tc.watch.result()
		if got.Outcome != tc.want || len(got.Warnings) != tc.wantWarnings {
			t.Errorf("%s: outcome %q with %d warnings, want %q with %d", tc.name, got.Outcome, len(got.Warnings), tc.want, tc.wantWarnings)
		}
	}
}
