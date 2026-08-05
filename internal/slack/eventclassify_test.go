package slack

import (
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// classifyScript runs the fixture through the classifier and tallies kinds —
// the fixture models the traps, so this pins the whole allowlist at once.
func classifyScript(t *testing.T) map[EventKind]int {
	t.Helper()
	byKind := map[EventKind]int{}
	for _, frame := range mockslack.DefaultEventScript() {
		if event, ok := ClassifyFrame(frame); ok {
			byKind[event.Kind]++
		}
	}
	return byKind
}

func TestClassifyDropsBookkeepingFrames(t *testing.T) {
	byKind := classifyScript(t)
	total := 0
	for _, n := range byKind {
		total += n
	}
	script := mockslack.DefaultEventScript()
	if total >= len(script) {
		t.Fatalf("classified %d of %d frames; the socket is mostly bookkeeping and it must be dropped", total, len(script))
	}
	// Everything the fixture models as real activity, and nothing else.
	want := map[EventKind]int{
		EventMessage:         4, // channel, thread reply, bot post, DM
		EventMessageChanged:  1,
		EventMessageDeleted:  1,
		EventReactionAdded:   2,
		EventReactionRemoved: 1,
	}
	for kind, n := range want {
		if byKind[kind] != n {
			t.Errorf("%s = %d, want %d (all: %v)", kind, byKind[kind], n, byKind)
		}
	}
}

// The parent re-send that follows every thread reply must never surface —
// forwarding it re-emits an old message as though it were new.
func TestClassifyDropsMessageReplied(t *testing.T) {
	frame := mockslack.WSMessageReplied(mockslack.WSChannelID, mockslack.WSUserID,
		"parent", "1700000010.000100", "1700000020.000200")
	if event, ok := ClassifyFrame(frame); ok {
		t.Fatalf("message_replied classified as %+v", event)
	}
}

func TestClassifyBotMessageKeepsAppAuthor(t *testing.T) {
	frame := mockslack.WSBotMessage(mockslack.WSChannelID, "Fabricated App", "deploy finished", "1700000025.000100")
	event, ok := ClassifyFrame(frame)
	if !ok {
		t.Fatal("a bot message is a message")
	}
	if !event.IsBot() || event.Author.UserID != "" {
		t.Errorf("author = %+v, want a bot id and no user id", event.Author)
	}
	if event.AuthorID() == "" || event.Content != "deploy finished" {
		t.Errorf("event = %+v", event)
	}
}

func TestClassifyEditCarriesBothBodies(t *testing.T) {
	frame := mockslack.WSMessageChanged(mockslack.WSChannelID, mockslack.WSOtherUser,
		"after", "1700000010.000100", "1700000030.000300")
	event, ok := ClassifyFrame(frame)
	if !ok {
		t.Fatal("message_changed should classify")
	}
	if event.Kind != EventMessageChanged || event.Content != "after" {
		t.Errorf("event = %+v", event)
	}
	if event.PreviousContent == "" {
		t.Error("an edit without its previous body cannot be reviewed")
	}
	// ts points at the edited message; the edit's own time is event_ts.
	if event.TS != "1700000010.000100" || event.EventTS != "1700000030.000300" {
		t.Errorf("ts = %q, event_ts = %q", event.TS, event.EventTS)
	}
}

func TestClassifyDeleteReportsDeletedTS(t *testing.T) {
	frame := mockslack.WSMessageDeleted(mockslack.WSChannelID, "1700000020.000200", "1700000050.000500")
	event, ok := ClassifyFrame(frame)
	if !ok {
		t.Fatal("message_deleted should classify")
	}
	if event.TS != "1700000020.000200" || event.Cursor() != "1700000050.000500" {
		t.Errorf("event = %+v", event)
	}
}

func TestClassifyReactionTargetsTheMessage(t *testing.T) {
	frame := mockslack.WSReactionAdded(mockslack.WSChannelID, mockslack.WSOtherUser,
		"eyes", "1700000010.000100", "1700000040.000400")
	event, ok := ClassifyFrame(frame)
	if !ok {
		t.Fatal("reaction_added should classify")
	}
	if event.ChannelID != mockslack.WSChannelID || event.TS != "1700000010.000100" {
		t.Errorf("a reaction points at the message it targets, got %+v", event)
	}
	if event.Cursor() != "1700000040.000400" {
		t.Errorf("cursor should be when the reaction happened, got %q", event.Cursor())
	}
}

func TestNormalizeReactionName(t *testing.T) {
	cases := map[string]string{
		"+1::skin-tone-3":    "+1",
		"+1":                 "+1",
		":white_check_mark:": "white_check_mark",
		" eyes ":             "eyes",
	}
	for input, want := range cases {
		if got := NormalizeReactionName(input); got != want {
			t.Errorf("NormalizeReactionName(%q) = %q, want %q", input, got, want)
		}
	}
}
