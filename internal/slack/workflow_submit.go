package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
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
	ResponseAction      string `json:"response_action,omitempty"`
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

// buildFormState maps the opened view's block element action_ids (field
// UUIDs) back to schema fields, then to the user-supplied values — the shape
// views.submit expects, mirroring each element's own input type. A supplied
// field that maps to no block errors rather than silently shrinking the
// submission (a stub view would otherwise submit an empty form). Pure, so
// form-layout learnings are table-testable on recorded view payloads.
func buildFormState(view map[string]any, schema WorkflowSchema, fields map[string]string) (map[string]any, error) {
	stateValues := map[string]any{}
	matched := map[string]bool{}
	for _, block := range recItems(getArr(view, "blocks")) {
		blockID := getStr(block, "block_id")
		element := getRec(block, "element")
		actionID := getStr(element, "action_id")
		if blockID == "" || actionID == "" {
			continue
		}
		schemaField := findSchemaField(schema, actionID)
		if schemaField == nil {
			continue
		}
		value := lookupField(fields, schemaField.Title)
		if value == nil {
			continue
		}
		entry, err := formStateEntry(element, schemaField.Title, *value)
		if err != nil {
			return nil, err
		}
		matched[strings.ToLower(schemaField.Title)] = true
		stateValues[blockID] = map[string]any{actionID: entry}
	}
	for title := range fields {
		if !matched[strings.ToLower(title)] {
			return nil, agenterrors.Newf(agenterrors.FixableByRetry,
				"field %q is not present in the opened form — the view may not have loaded fully", title).
				WithHint("retry the run; 'workflow get' re-checks the field titles")
		}
	}
	return stateValues, nil
}

func findSchemaField(schema WorkflowSchema, actionID string) *FormField {
	for i := range schema.Fields {
		if schema.Fields[i].Name == actionID {
			return &schema.Fields[i]
		}
	}
	return nil
}

// formStateEntry builds one state entry in the shape the element's input type
// expects — views.submit rejects state whose type does not match the rendered
// element, and reports it only via response_action "errors".
func formStateEntry(element map[string]any, title, value string) (map[string]any, error) {
	elemType := FirstNonEmpty(getStr(element, "type"), "plain_text_input")
	switch elemType {
	case "plain_text_input", "number_input", "email_text_input", "url_text_input":
		return map[string]any{"type": elemType, "value": value}, nil
	case "rich_text_input":
		return map[string]any{"type": elemType, "rich_text_value": richTextValue(value)}, nil
	case "static_select", "radio_buttons":
		opt, err := matchElementOption(element, title, value)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": elemType, "selected_option": opt}, nil
	case "checkboxes":
		var opts []any
		for _, part := range strings.Split(value, ",") {
			opt, err := matchElementOption(element, title, strings.TrimSpace(part))
			if err != nil {
				return nil, err
			}
			opts = append(opts, opt)
		}
		return map[string]any{"type": elemType, "selected_options": opts}, nil
	case "datepicker":
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return nil, agenterrors.Newf(agenterrors.FixableByAgent,
				"field %q expects a date, got %q", title, value).
				WithHint("use YYYY-MM-DD and rerun — this run was abandoned without submitting")
		}
		return map[string]any{"type": elemType, "selected_date": value}, nil
	case "timepicker":
		return map[string]any{"type": elemType, "selected_time": value}, nil
	default:
		return nil, agenterrors.Newf(agenterrors.FixableByHuman,
			"field %q is a %s input, which agent-slack cannot submit", title, elemType).
			WithHint("this run was abandoned without submitting; use a Slack client for this workflow's form")
	}
}

// richTextValue wraps a plain string in the minimal rich_text document a
// rich_text_input element expects.
func richTextValue(value string) map[string]any {
	return map[string]any{
		"type": "rich_text",
		"elements": []any{map[string]any{
			"type": "rich_text_section",
			"elements": []any{map[string]any{"type": "text", "text": value}},
		}},
	}
}

// matchElementOption finds the element option whose value or label matches
// (labels case-insensitively) and returns the option object verbatim —
// views.submit expects the full option, text object included. Grouped options
// (option_groups) are flattened in.
func matchElementOption(element map[string]any, title, value string) (map[string]any, error) {
	options := recItems(getArr(element, "options"))
	for _, group := range recItems(getArr(element, "option_groups")) {
		options = append(options, recItems(getArr(group, "options"))...)
	}
	var labels []string
	for _, opt := range options {
		label := getStr(getRec(opt, "text"), "text")
		if getStr(opt, "value") == value || strings.EqualFold(label, value) {
			return opt, nil
		}
		labels = append(labels, label)
	}
	return nil, agenterrors.Newf(agenterrors.FixableByAgent,
		"field %q has no option matching %q. Available: %s", title, value, strings.Join(labels, ", ")).
		WithHint("match an option by its label or value and rerun — this run was abandoned without submitting")
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

// rtmConn's ReadJSON must return once ctx is done — it is the await loop's
// only unblock path.
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

	var tripResult WorkflowRunResult
	viewMsg, err := awaitOpenedView(ctx, c, conn, func() error {
		var terr error
		tripResult, terr = RunWorkflowTrigger(ctx, c, input.ShortcutURL, input.ChannelID, input.BookmarkID)
		return terr
	})
	if err != nil {
		return WorkflowSubmitResult{}, err
	}

	view := getRec(viewMsg, "view")
	viewID := getStr(view, "id")
	if viewID == "" {
		return WorkflowSubmitResult{}, agenterrors.New("view_opened event did not contain a view_id", agenterrors.FixableByRetry)
	}
	// From here the form is open server-side: every giving-up path must
	// abandon it, so the cleanup is owned by one success-gated defer.
	submitted := false
	defer func() {
		if !submitted {
			abandonView(ctx, c, viewID)
		}
	}()
	view = fetchOpenedView(ctx, c, viewID, view)

	state, err := buildFormState(view, input.Schema, input.Fields)
	if err != nil {
		return WorkflowSubmitResult{}, err
	}

	stateJSON, _ := json.Marshal(map[string]any{"values": state})
	resp, err := c.API(ctx, "views.submit", map[string]any{
		"view_id":      viewID,
		"client_token": fmt.Sprintf("cli-%d", time.Now().UnixMilli()),
		"state":        string(stateJSON),
	})
	if err != nil {
		return WorkflowSubmitResult{}, err
	}
	if rejectErr := submitRejection(resp, view, input.Schema); rejectErr != nil {
		return WorkflowSubmitResult{}, rejectErr
	}

	submitted = true
	return WorkflowSubmitResult{
		FunctionExecutionID: tripResult.FunctionExecutionID,
		TriggerExecutionID:  tripResult.TriggerExecutionID,
		ViewID:              viewID,
		ResponseAction:      getStr(resp, "response_action"),
		Submitted:           true,
	}, nil
}

// fetchOpenedView fetches the authoritative view via views.get — the real
// client re-fetches after tripping rather than trusting the push payload,
// which can be a stub when several clients share the session. Best-effort:
// any failure falls back to the event's view.
func fetchOpenedView(ctx context.Context, c *Client, viewID string, eventView map[string]any) map[string]any {
	resp, err := c.API(ctx, "views.get", map[string]any{"view_id": viewID})
	if err != nil {
		return eventView
	}
	view := getRec(resp, "view")
	if len(getArr(view, "blocks")) == 0 {
		return eventView
	}
	return view
}

// submitRejection interprets an ok:true views.submit body. Block Kit reports
// modal validation failures as response_action "errors" plus a block_id-keyed
// errors map, not ok:false — treating bare ok as success silently drops the
// run. Block ids map back to field titles best-effort, falling back to the
// raw id.
func submitRejection(resp, view map[string]any, schema WorkflowSchema) error {
	errsByBlock := getRec(resp, "errors")
	if getStr(resp, "response_action") != "errors" && len(errsByBlock) == 0 {
		return nil
	}
	parts := make([]string, 0, len(errsByBlock))
	for _, blockID := range slices.Sorted(maps.Keys(errsByBlock)) {
		label := blockID
		if title := blockFieldTitle(view, schema, blockID); title != "" {
			label = title
		}
		parts = append(parts, fmt.Sprintf("%s: %v", label, errsByBlock[blockID]))
	}
	detail := "no field errors were reported"
	if len(parts) > 0 {
		detail = strings.Join(parts, "; ")
	}
	return agenterrors.Newf(agenterrors.FixableByAgent,
		"the workflow form rejected the submission: %s", detail).
		WithHint("fix the field values and rerun — this run did not complete")
}

func blockFieldTitle(view map[string]any, schema WorkflowSchema, blockID string) string {
	for _, block := range recItems(getArr(view, "blocks")) {
		if getStr(block, "block_id") != blockID {
			continue
		}
		if f := findSchemaField(schema, getStr(getRec(block, "element"), "action_id")); f != nil {
			return f.Title
		}
	}
	return ""
}

// abandonView best-effort closes a form view whose submission is being given
// up on. Workflow form views set notify_on_close, so the close cancels the
// tripped run instead of leaving a dangling modal on the user's other
// clients.
func abandonView(ctx context.Context, c *Client, viewID string) {
	if _, err := c.API(ctx, "views.close", map[string]any{"view_id": viewID}); err != nil {
		c.debugf("views.close %s failed: %v", viewID, err)
	}
}

// awaitOpenedView listens for the workflow's view_opened/view_push on the RTM
// connection, then returns the opened view. Listening starts BEFORE trip fires
// the trigger, because the event can arrive before the trip call returns; the
// wait is bounded by a 15s timeout. trip is the trigger-tripping side effect
// (kept as a callback so the listen-before-trip ordering lives in one place).
func awaitOpenedView(ctx context.Context, c *Client, conn rtmConn, trip func() error) (map[string]any, error) {
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
			c.debugFrame(msg)
			if t := getStr(msg, "type"); t == "view_opened" || t == "view_push" {
				viewCh <- msg
				return
			}
		}
	}()

	if err := trip(); err != nil {
		return nil, err
	}

	// A timeout or read error surfaces as the goroutine closing viewCh, so a
	// plain receive is the single exit — no deadline race against a view that
	// arrives right at the boundary.
	msg, ok := <-viewCh
	if !ok {
		return nil, agenterrors.New("timed out waiting for the workflow form (view_opened)", agenterrors.FixableByRetry)
	}
	return msg, nil
}
