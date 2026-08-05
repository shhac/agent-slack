package mockslack

import (
	"regexp"
	"strings"
	"testing"
)

// The default script exists to expose the shapes that break a naive consumer.
// These assertions pin the traps so a future edit cannot quietly sand them off
// and leave the fixture agreeable but useless.

func TestDefaultScriptPairsThreadReplyWithParentResend(t *testing.T) {
	var replies, resends int
	for _, frame := range DefaultEventScript() {
		if frame["type"] != "message" {
			continue
		}
		if frame["subtype"] == "message_replied" {
			resends++
			if frame["hidden"] != true {
				t.Error("message_replied should be hidden")
			}
			continue
		}
		if frame["thread_ts"] != nil && frame["subtype"] == nil {
			replies++
		}
	}
	if replies == 0 || replies != resends {
		t.Fatalf("thread replies = %d, parent re-sends = %d; a reply produces both", replies, resends)
	}
}

func TestBotMessageHasNoUserField(t *testing.T) {
	frame := WSBotMessage(WSChannelID, "Fabricated App", "text", "1700000000.000100")
	if _, ok := frame["user"]; ok {
		t.Error("bot messages carry bot_id/username, not user — a fixture with `user` hides the trap")
	}
	if frame["bot_id"] == nil || frame["username"] == nil {
		t.Errorf("bot message missing author fields: %v", frame)
	}
}

// Socket frames are not Events API payloads: no envelope, no channel_type.
func TestFramesOmitEventsAPIOnlyFields(t *testing.T) {
	for _, frame := range DefaultEventScript() {
		for _, key := range []string{"channel_type", "event", "authorizations", "api_app_id"} {
			if _, ok := frame[key]; ok {
				t.Errorf("frame %v carries Events-API-only field %q", frame["type"], key)
			}
		}
	}
}

func TestPlainMessagesCarryBlocks(t *testing.T) {
	frame := WSMessage(WSChannelID, WSOtherUser, "hello", "1700000000.000100")
	blocks, ok := frame["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("message should carry rich_text blocks, got %v", frame["blocks"])
	}
	if block, _ := blocks[0].(map[string]any); block["type"] != "rich_text" {
		t.Errorf("first block = %v, want rich_text", blocks[0])
	}
}

// Fixture ids must parse as real Slack ids (kind prefix + uppercase
// alphanumerics, 9+ chars). This is not cosmetic: an id with an underscore is
// not recognised as an id at all, so every test using it silently falls
// through to a name-lookup path and exercises code the real flow never runs.
func TestFixtureIDsAreShapedLikeSlackIDs(t *testing.T) {
	idRe := regexp.MustCompile(`^[CDGUBTA][A-Z0-9]{8,}$`)
	for _, id := range []string{WSTeamID, WSChannelID, WSDMID, WSUserID, WSOtherUser, WSBotID, WSAppID} {
		if !idRe.MatchString(id) {
			t.Errorf("fixture id %q is not Slack-shaped; tests using it take a different code path", id)
		}
	}
	// And they must stay obviously invented — never a real workspace's ids.
	for _, id := range []string{WSTeamID, WSChannelID, WSDMID, WSUserID, WSOtherUser, WSBotID, WSAppID} {
		if !strings.Contains(id, "FAKE") {
			t.Errorf("fixture id %q should be self-evidently fabricated", id)
		}
	}
}
