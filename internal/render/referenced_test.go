package render

import (
	"reflect"
	"testing"
)

func TestCollectReferencedUserIDs(t *testing.T) {
	messages := []MessageSummary{
		{
			User: "U11111111",
			Text: "hi <@U22222222> and <@W33333333|nick>",
			Blocks: []any{map[string]any{
				"type": "rich_text",
				"elements": []any{map[string]any{
					"type":     "rich_text_section",
					"elements": []any{map[string]any{"type": "user", "user_id": "U44444444"}},
				}},
			}},
			Attachments: []any{map[string]any{
				"text": "from <@U22222222>", // duplicate — deduped
				"user": "U55555555",
			}},
			Reactions: []any{map[string]any{
				"name":  "eyes",
				"users": []any{"U66666666"},
			}},
		},
	}

	withoutReactions := CollectReferencedUserIDs(messages, false)
	want := []string{"U11111111", "U22222222", "W33333333", "U44444444", "U55555555"}
	if !reflect.DeepEqual(withoutReactions, want) {
		t.Errorf("got %v, want %v", withoutReactions, want)
	}

	withReactions := CollectReferencedUserIDs(messages, true)
	if !reflect.DeepEqual(withReactions, append(want, "U66666666")) {
		t.Errorf("got %v", withReactions)
	}
}

func TestCollectReferencedUserIDsIgnoresInvalid(t *testing.T) {
	messages := []MessageSummary{{
		User: "not-an-id",
		Text: "<@U1234> too short, U22222222 bare (not a mention token)",
		Blocks: []any{map[string]any{
			"user_id": 42, // non-string
			"users":   []any{"C12345678"},
		}},
	}}
	if got := CollectReferencedUserIDs(messages, true); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
