package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// WorkflowSubmitResult is the outcome of a form-submitting workflow run.
type WorkflowSubmitResult struct {
	FunctionExecutionID string `json:"function_execution_id,omitempty"`
	TriggerExecutionID  string `json:"trigger_execution_id,omitempty"`
	ViewID              string `json:"view_id"`
	Submitted           bool   `json:"submitted"`
}

// ValidateWorkflowFields checks user-supplied Title=value pairs against the
// schema (case-insensitive on titles) and returns the problems found.
func ValidateWorkflowFields(fields map[string]string, schema WorkflowSchema) []string {
	var errs []string
	titles := make([]string, 0, len(schema.Fields))
	known := map[string]bool{}
	for _, f := range schema.Fields {
		titles = append(titles, f.Title)
		known[strings.ToLower(f.Title)] = true
	}
	for title := range fields {
		if !known[strings.ToLower(title)] {
			errs = append(errs, fmt.Sprintf("unknown field %q. Available: %s", title, strings.Join(titles, ", ")))
		}
	}
	for _, f := range schema.Fields {
		if f.Required && lookupField(fields, f.Title) == nil {
			errs = append(errs, fmt.Sprintf("required field %q is missing", f.Title))
		}
	}
	return errs
}

func lookupField(fields map[string]string, title string) *string {
	if v, ok := fields[title]; ok {
		return &v
	}
	lower := strings.ToLower(title)
	for k, v := range fields {
		if strings.ToLower(k) == lower {
			return &v
		}
	}
	return nil
}

// dialRTM is a seam so tests can fake the WebSocket.
var dialRTM = func(ctx context.Context, wsURL, cookie string) (rtmConn, error) {
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{cookie}},
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(4 << 20)
	return &websocketConn{conn: conn}, nil
}

type rtmConn interface {
	ReadJSON(ctx context.Context) (map[string]any, error)
	Close()
}

type websocketConn struct{ conn *websocket.Conn }

func (w *websocketConn) ReadJSON(ctx context.Context) (map[string]any, error) {
	_, data, err := w.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, nil // non-JSON frames are skipped, not fatal
	}
	return msg, nil
}

func (w *websocketConn) Close() { _ = w.conn.Close(websocket.StatusNormalClosure, "") }

// WorkflowSubmission is the input to SubmitWorkflowForm.
type WorkflowSubmission struct {
	ShortcutURL string
	ChannelID   string
	BookmarkID  string
	Fields      map[string]string
	Schema      WorkflowSchema
}

// SubmitWorkflowForm trips a workflow whose first step opens a form, captures
// the resulting view over a short-lived RTM WebSocket, and submits the field
// values. Requires browser auth: views.submit and rtm.connect are client
// APIs.
func SubmitWorkflowForm(ctx context.Context, c *Client, input WorkflowSubmission) (WorkflowSubmitResult, error) {
	auth := c.currentAuth()
	if auth.Type != AuthBrowser {
		return WorkflowSubmitResult{}, agenterrors.New(
			"form submission requires browser auth (xoxc/xoxd); standard bot tokens cannot submit workflow forms",
			agenterrors.FixableByHuman).WithHint("import browser credentials with 'agent-slack auth import-desktop'")
	}

	rtmResp, err := c.API(ctx, "rtm.connect", nil)
	if err != nil {
		return WorkflowSubmitResult{}, err
	}
	wsURL := getStr(rtmResp, "url")
	if wsURL == "" {
		return WorkflowSubmitResult{}, agenterrors.New("rtm.connect did not return a WebSocket URL", agenterrors.FixableByRetry)
	}

	conn, err := dialRTM(ctx, wsURL, "d="+url.QueryEscape(auth.XOXD))
	if err != nil {
		return WorkflowSubmitResult{}, agenterrors.Wrap(err, agenterrors.FixableByRetry).
			WithHint("could not open the RTM WebSocket — retry")
	}
	defer conn.Close()

	// Start listening BEFORE tripping: view_opened can arrive before the trip
	// call returns.
	viewCh := make(chan map[string]any, 1)
	listenCtx, cancelListen := context.WithTimeout(ctx, 15*time.Second)
	defer cancelListen()
	go func() {
		for {
			msg, rerr := conn.ReadJSON(listenCtx)
			if rerr != nil {
				close(viewCh)
				return
			}
			if msg == nil {
				continue
			}
			if t := getStr(msg, "type"); t == "view_opened" || t == "view_push" {
				viewCh <- msg
				return
			}
		}
	}()

	tripResult, err := RunWorkflowTrigger(ctx, c, input.ShortcutURL, input.ChannelID, input.BookmarkID)
	if err != nil {
		return WorkflowSubmitResult{}, err
	}

	var viewMsg map[string]any
	select {
	case msg, ok := <-viewCh:
		if !ok {
			return WorkflowSubmitResult{}, agenterrors.New("timed out waiting for the workflow form (view_opened)", agenterrors.FixableByRetry)
		}
		viewMsg = msg
	case <-listenCtx.Done():
		return WorkflowSubmitResult{}, agenterrors.New("timed out waiting for the workflow form (view_opened)", agenterrors.FixableByRetry)
	}

	view := getRec(viewMsg, "view")
	viewID := getStr(view, "id")
	if viewID == "" {
		return WorkflowSubmitResult{}, agenterrors.New("view_opened event did not contain a view_id", agenterrors.FixableByRetry)
	}

	// Map view block element action_ids (field UUIDs) back to schema fields,
	// then to the user-supplied values.
	stateValues := map[string]any{}
	for _, block := range recItems(getArr(view, "blocks")) {
		blockID := getStr(block, "block_id")
		actionID := getStr(getRec(block, "element"), "action_id")
		if blockID == "" || actionID == "" {
			continue
		}
		var schemaField *FormField
		for i := range input.Schema.Fields {
			if input.Schema.Fields[i].Name == actionID {
				schemaField = &input.Schema.Fields[i]
				break
			}
		}
		if schemaField == nil {
			continue
		}
		value := lookupField(input.Fields, schemaField.Title)
		if value == nil {
			continue
		}
		stateValues[blockID] = map[string]any{
			actionID: map[string]any{"type": "plain_text_input", "value": *value},
		}
	}

	stateJSON, _ := json.Marshal(map[string]any{"values": stateValues})
	if _, err := c.API(ctx, "views.submit", map[string]any{
		"view_id":      viewID,
		"client_token": fmt.Sprintf("cli-%d", time.Now().UnixMilli()),
		"state":        string(stateJSON),
	}); err != nil {
		return WorkflowSubmitResult{}, err
	}

	return WorkflowSubmitResult{
		FunctionExecutionID: tripResult.FunctionExecutionID,
		TriggerExecutionID:  tripResult.TriggerExecutionID,
		ViewID:              viewID,
		Submitted:           true,
	}, nil
}
