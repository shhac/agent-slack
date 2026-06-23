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

	withoutReactions := CollectReferencedIDs(messages, false).Users
	want := []string{"U11111111", "U22222222", "W33333333", "U44444444", "U55555555"}
	if !reflect.DeepEqual(withoutReactions, want) {
		t.Errorf("got %v, want %v", withoutReactions, want)
	}

	withReactions := CollectReferencedIDs(messages, true).Users
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
	if got := CollectReferencedIDs(messages, true).Users; len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestCollectReferencedIDsChannelsAndUsergroups(t *testing.T) {
	messages := []MessageSummary{
		{
			Text: "see <#C12345678|general> and ping <!subteam^S12345678|@team>",
			Blocks: []any{map[string]any{
				"type": "rich_text",
				"elements": []any{map[string]any{
					"type": "rich_text_section",
					"elements": []any{
						map[string]any{"type": "channel", "channel_id": "C87654321"},
						map[string]any{"type": "usergroup", "usergroup_id": "S87654321"},
					},
				}},
			}},
		},
	}
	got := CollectReferencedIDs(messages, false)
	if want := []string{"C12345678", "C87654321"}; !reflect.DeepEqual(got.Channels, want) {
		t.Errorf("channels = %v, want %v", got.Channels, want)
	}
	if want := []string{"S12345678", "S87654321"}; !reflect.DeepEqual(got.Usergroups, want) {
		t.Errorf("usergroups = %v, want %v", got.Usergroups, want)
	}
}
