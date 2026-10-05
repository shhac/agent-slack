package slack

import (
	"encoding/json"
	"fmt"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// ActionSelector names one interactive element on a message: Label matches a
// button's text or a menu's placeholder (case-insensitive) or an action_id;
// ActionID matches exactly; BlockID narrows either.
type ActionSelector struct {
	Label    string
	ActionID string
	BlockID  string
}

// SelectMessageAction finds the single element sel names among a message's
// blocks. Every miss is agent-fixable and lists what the message offers.
func SelectMessageAction(msg map[string]any, sel ActionSelector) (render.InteractiveElement, error) {
	elements := render.InteractiveElements(getArr(msg, "blocks"))
	if len(elements) == 0 {
		return render.InteractiveElement{}, noActionsError(msg)
	}
	if sel.Label == "" && sel.ActionID == "" {
		return render.InteractiveElement{}, agenterrors.New("name the button or menu to press", agenterrors.FixableByAgent).
			WithHint("pass its label or --action-id; this message offers " + describeCandidates(elements))
	}

	var matches []render.InteractiveElement
	for _, ie := range elements {
		if elementMatches(ie, sel) {
			matches = append(matches, ie)
		}
	}
	switch len(matches) {
	case 1:
		if url := getStr(matches[0].Element, "url"); url != "" {
			return render.InteractiveElement{}, agenterrors.Newf(agenterrors.FixableByAgent,
				"%s only opens a link, so pressing it does nothing an agent can use", describeElement(matches[0])).
				WithHint("fetch the link instead: " + url)
		}
		return matches[0], nil
	case 0:
		return render.InteractiveElement{}, agenterrors.Newf(agenterrors.FixableByAgent,
			"no button or menu matching %s on this message", describeSelector(sel)).
			WithHint("this message offers " + describeCandidates(elements))
	default:
		return render.InteractiveElement{}, agenterrors.Newf(agenterrors.FixableByAgent,
			"%d buttons or menus match %s", len(matches), describeSelector(sel)).
			WithHint("narrow with --block-id or --action-id: " + describeCandidates(matches))
	}
}

func elementMatches(ie render.InteractiveElement, sel ActionSelector) bool {
	if sel.BlockID != "" && ie.BlockID != sel.BlockID {
		return false
	}
	actionID := getStr(ie.Element, "action_id")
	if sel.ActionID != "" && actionID != sel.ActionID {
		return false
	}
	if sel.Label == "" {
		return true
	}
	label := strings.TrimSpace(sel.Label)
	return strings.EqualFold(render.ElementLabel(ie.Element), label) || actionID == label
}

func describeSelector(sel ActionSelector) string {
	var parts []string
	if sel.Label != "" {
		parts = append(parts, fmt.Sprintf("%q", sel.Label))
	}
	if sel.ActionID != "" {
		parts = append(parts, "action_id "+sel.ActionID)
	}
	if sel.BlockID != "" {
		parts = append(parts, "block_id "+sel.BlockID)
	}
	return strings.Join(parts, " in ")
}

func describeCandidates(elements []render.InteractiveElement) string {
	parts := make([]string, 0, len(elements))
	for _, ie := range elements {
		parts = append(parts, describeElement(ie))
	}
	return strings.Join(parts, ", ")
}

// describeElement is the one-line address of an element: its label, then
// block_id/action_id so a caller can pick it unambiguously.
func describeElement(ie render.InteractiveElement) string {
	label := render.ElementLabel(ie.Element)
	addr := fmt.Sprintf("%s/%s", ie.BlockID, getStr(ie.Element, "action_id"))
	if label == "" {
		return fmt.Sprintf("%s (%s)", getStr(ie.Element, "type"), addr)
	}
	return fmt.Sprintf("%q (%s %s)", label, getStr(ie.Element, "type"), addr)
}

// DescribeElement is describeElement for callers outside the package.
func DescribeElement(ie render.InteractiveElement) string {
	return describeElement(ie)
}

func noActionsError(msg map[string]any) error {
	err := agenterrors.New("this message has no buttons or menus to press", agenterrors.FixableByAgent)
	for _, a := range recItems(getArr(msg, "attachments")) {
		if len(getArr(a, "actions")) > 0 {
			return err.WithHint("its buttons are legacy attachment actions, which 'message action' does not support")
		}
	}
	return err.WithHint("'message get' lists a message's interactive elements under 'actions'")
}

// blockActionParams builds the blocks.actions form the web client sends: the
// app is addressed by the posting bot (service_id) and its team, the element
// by its block/action ids, and the message by a container like the one Slack
// hands the app in its block_actions payload.
func blockActionParams(in PressInput) (map[string]any, error) {
	serviceID := FirstNonEmpty(getStr(in.Message, "bot_id"), getStr(in.Message, "app_id"))
	if serviceID == "" {
		return nil, agenterrors.New("this message was not posted by an app, so there is nothing listening for the press",
			agenterrors.FixableByAgent).WithHint("only app (bot) messages have pressable buttons")
	}
	actions, _ := json.Marshal([]map[string]any{actionPayload(in.Target, in.Choice)})
	container, _ := json.Marshal(map[string]any{
		"type":         "message",
		"message_ts":   in.Ref.MessageTS,
		"channel_id":   in.Ref.ChannelID,
		"is_ephemeral": false,
	})
	params := map[string]any{
		"service_id":   serviceID,
		"actions":      string(actions),
		"container":    string(container),
		"client_token": clientToken(),
	}
	if team := FirstNonEmpty(getStr(getRec(in.Message, "bot_profile"), "team_id"), getStr(in.Message, "team")); team != "" {
		params["service_team_id"] = team
	}
	return params, nil
}

// actionPayload is the element as the app expects to receive it back: its
// address, type, the fields an app keys behaviour on, and — for a menu or
// picker — the chosen value under the element type's own key.
func actionPayload(ie render.InteractiveElement, chosen map[string]any) map[string]any {
	el := ie.Element
	payload := map[string]any{
		"block_id":  ie.BlockID,
		"action_id": getStr(el, "action_id"),
		"type":      getStr(el, "type"),
	}
	for _, key := range []string{"text", "value", "style", "placeholder"} {
		if v, ok := el[key]; ok {
			payload[key] = v
		}
	}
	for key, v := range chosen {
		payload[key] = v
	}
	return payload
}

// ActionChoice validates the value a press supplies against the element: a
// button takes none, and every menu or picker needs one — Slack sends the
// selection, not the click. It runs before the press, so a bad value never
// reaches the app.
func ActionChoice(ie render.InteractiveElement, value string) (map[string]any, error) {
	elemType := getStr(ie.Element, "type")
	label := FirstNonEmpty(render.ElementLabel(ie.Element), getStr(ie.Element, "action_id"))
	if elemType == "button" {
		if value != "" {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent, "%s is a button, which takes no --value", describeElement(ie)).
				WithHint("drop --value to press it")
		}
		return nil, nil
	}
	if value == "" {
		hint := "pass the choice with --value"
		if labels := render.CompactActionFor(ie).Options; len(labels) > 0 {
			hint += "; options: " + strings.Join(labels, ", ")
		}
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "%s is a %s and needs a --value", describeElement(ie), elemType).
			WithHint(hint)
	}
	entry, err := formStateEntry(ie.Element, label, value, menuValues)
	if err != nil {
		return nil, err
	}
	delete(entry, "type")
	return entry, nil
}
