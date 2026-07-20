package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

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

	conn, err := c.dialRTM(ctx, wsURL, "d="+url.QueryEscape(auth.XOXD))
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

	state, titlesByBlock, err := buildFormState(view, input.Schema, input.Fields)
	if err != nil {
		return WorkflowSubmitResult{}, err
	}

	stateJSON, _ := json.Marshal(map[string]any{"values": state})
	resp, err := c.API(ctx, "views.submit", map[string]any{
		"view_id":      viewID,
		"client_token": clientToken(),
		"state":        string(stateJSON),
	})
	if err != nil {
		return WorkflowSubmitResult{}, err
	}
	if rejectErr := submitRejection(resp, titlesByBlock); rejectErr != nil {
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
// run. Block ids resolve to field titles via the mapping buildFormState
// produced, falling back to the raw id.
func submitRejection(resp map[string]any, titlesByBlock map[string]string) error {
	errsByBlock := getRec(resp, "errors")
	if getStr(resp, "response_action") != "errors" && len(errsByBlock) == 0 {
		return nil
	}
	parts := make([]string, 0, len(errsByBlock))
	for _, blockID := range slices.Sorted(maps.Keys(errsByBlock)) {
		label := FirstNonEmpty(titlesByBlock[blockID], blockID)
		parts = append(parts, fmt.Sprintf("%s: %v", label, errsByBlock[blockID]))
	}
	detail := FirstNonEmpty(strings.Join(parts, "; "), "no field errors were reported")
	return agenterrors.Newf(agenterrors.FixableByAgent,
		"the workflow form rejected the submission: %s", detail).
		WithHint("fix the field values and rerun — this run did not complete")
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
			c.debugJSON("RTM frame", msg)
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
