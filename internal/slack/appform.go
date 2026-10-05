package slack

// Settling a modal an app opened in response to a press: filling it in when
// the caller named fields, otherwise describing and closing it.

import (
	"context"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// SettleOpenedView deals with a view a press opened. With fields it is
// filled in and submitted; without them, or when filling fails, it is
// described and closed — left open it would linger on the user's other
// clients. A submission that moves the form on ("update"/"push") leaves a
// further step open, which is closed and named in the returned warning.
func SettleOpenedView(ctx context.Context, c *Client, pushed map[string]any, fields map[string]string) (ViewSummary, string) {
	view := fetchOpenedView(ctx, c, pushed)
	summary := describeView(view)
	if len(fields) == 0 {
		summary.Closed = closeView(ctx, c, summary.ID)
		return summary, ""
	}
	responseAction, err := submitAppForm(ctx, c, view, fields)
	if err != nil {
		summary.Error = errorWithHint(err)
		summary.Closed = closeView(ctx, c, summary.ID)
		return summary, ""
	}
	summary.Submitted = true
	summary.ResponseAction = responseAction
	if responseAction == "update" || responseAction == "push" {
		summary.Closed = closeView(ctx, c, summary.ID)
		return summary, "the form moved to another step, which was closed — multi-step forms are not supported"
	}
	summary.Closed = true
	return summary, ""
}

// submitAppForm fills a view with fields (label → value), submits it, and
// returns Slack's response_action. A field problem or a Slack-side rejection
// is an error, and nothing is left submitted.
func submitAppForm(ctx context.Context, c *Client, view map[string]any, fields map[string]string) (string, error) {
	state, titlesByBlock, err := buildViewState(view, fields)
	if err != nil {
		return "", err
	}
	resp, err := submitViewState(ctx, c, getStr(view, "id"), state)
	if err != nil {
		var apiErr *agenterrors.APIError
		if agenterrors.As(err, &apiErr) && apiErr.FixableBy == agenterrors.FixableByRetry {
			return "", agenterrors.Wrap(err, agenterrors.FixableByAgent).
				WithHint("the form may have been submitted — check the message before pressing again")
		}
		return "", err
	}
	if detail, rejected := rejectedFields(resp, titlesByBlock); rejected {
		return "", agenterrors.Newf(agenterrors.FixableByAgent,
			"the app rejected the form: %s", detail).
			WithHint("fix the field values and press again — " + nothingSubmittedHint)
	}
	return getStr(resp, "response_action"), nil
}

// errorWithHint keeps an error's hint in a result field: for a submission
// that may have landed, the hint is what stops a second one.
func errorWithHint(err error) string {
	var apiErr *agenterrors.APIError
	if agenterrors.As(err, &apiErr) && apiErr.Hint != "" {
		return apiErr.Message + " — " + apiErr.Hint
	}
	return err.Error()
}
