package render

import (
	"reflect"
	"testing"
)

func TestMessageActionsCollectsInteractiveElements(t *testing.T) {
	blocks := []any{
		map[string]any{"type": "section", "block_id": "summary",
			"text": map[string]any{"type": "mrkdwn", "text": "Deploy 42 is waiting"},
			"accessory": map[string]any{"type": "overflow", "action_id": "more",
				"options": []any{
					map[string]any{"text": map[string]any{"type": "plain_text", "text": "Retry"}, "value": "retry"},
					map[string]any{"text": map[string]any{"type": "plain_text", "text": "Cancel"}, "value": "cancel"},
				}}},
		map[string]any{"type": "actions", "block_id": "decide", "elements": []any{
			map[string]any{"type": "button", "action_id": "approve", "style": "primary",
				"text": map[string]any{"type": "plain_text", "text": "Approve"},
				"confirm": map[string]any{
					"title": map[string]any{"type": "plain_text", "text": "Ship it?"},
					"text":  map[string]any{"type": "plain_text", "text": "This deploys to production."}}},
			map[string]any{"type": "button", "action_id": "logs",
				"text": map[string]any{"type": "plain_text", "text": "Logs"},
				"url":  "https://ci.example.invalid/run/42"},
			map[string]any{"type": "static_select", "action_id": "env",
				"placeholder": map[string]any{"type": "plain_text", "text": "Pick an environment"},
				"option_groups": []any{map[string]any{"options": []any{
					map[string]any{"text": map[string]any{"type": "plain_text", "text": "Staging"}, "value": "stg"},
				}}}},
			map[string]any{"type": "button", "text": map[string]any{"type": "plain_text", "text": "No id"}},
			map[string]any{"type": "image", "image_url": "https://example.invalid/x.png", "alt_text": "x"},
		}},
		map[string]any{"type": "section", "block_id": "plain",
			"text": map[string]any{"type": "mrkdwn", "text": "no accessory"}},
	}

	got := MessageActions(blocks)
	want := []CompactAction{
		{BlockID: "summary", ActionID: "more", Type: "overflow", Options: []string{"Retry", "Cancel"}},
		{BlockID: "decide", ActionID: "approve", Type: "button", Text: "Approve", Style: "primary",
			Confirm: "Ship it?: This deploys to production."},
		{BlockID: "decide", ActionID: "logs", Type: "button", Text: "Logs", URL: "https://ci.example.invalid/run/42"},
		{BlockID: "decide", ActionID: "env", Type: "static_select", Text: "Pick an environment", Options: []string{"Staging"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MessageActions =\n%+v\nwant\n%+v", got, want)
	}
}

func TestMessageActionsIsNilWithoutInteractiveElements(t *testing.T) {
	msg := MessageSummary{TS: "1700000010.000100", Blocks: []any{
		map[string]any{"type": "context", "elements": []any{
			map[string]any{"type": "mrkdwn", "text": "just prose"},
		}},
	}}
	if got := ToCompactMessage(msg, CompactOptions{}).Actions; got != nil {
		t.Errorf("Actions = %+v, want nil so the field is omitted", got)
	}
}

// A menu can carry hundreds of options; list rows keep only the count so a
// channel full of app cards does not balloon, and a single read restores them.
func TestCompactMessageCountsActionOptionsUnlessAsked(t *testing.T) {
	msg := MessageSummary{TS: "1700000010.000100", Blocks: []any{
		map[string]any{"type": "actions", "block_id": "b", "elements": []any{
			map[string]any{"type": "static_select", "action_id": "env", "options": []any{
				map[string]any{"text": map[string]any{"type": "plain_text", "text": "Staging"}, "value": "stg"},
				map[string]any{"text": map[string]any{"type": "plain_text", "text": "Production"}, "value": "prd"},
			}},
		}},
	}}

	listed := ToCompactMessage(msg, CompactOptions{}).Actions[0]
	if listed.Options != nil || listed.OptionCount != 2 {
		t.Errorf("list action = %+v, want no labels and option_count 2", listed)
	}
	got := ToCompactMessage(msg, CompactOptions{ActionOptions: true}).Actions[0]
	if !reflect.DeepEqual(got.Options, []string{"Staging", "Production"}) || got.OptionCount != 0 {
		t.Errorf("get action = %+v, want the labels and no count", got)
	}
}
