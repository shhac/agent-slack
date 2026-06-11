package slack

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/mockslack"
)

func newWorkflowClient(t *testing.T, server *mockslack.Server) *Client {
	t.Helper()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return New(Auth{Type: AuthStandard, Token: "xoxb-test", WorkspaceURL: ts.URL}, WithBaseURL(ts.URL))
}

func previewRejection(t *testing.T, code string) *agenterrors.APIError {
	t.Helper()
	server := mockslack.New()
	server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok":                true,
		"triggers":          []any{},
		"rejected_triggers": []any{map[string]any{"id": "Ft0898SEA5N0", "error": code}},
	})
	_, err := PreviewWorkflowTrigger(context.Background(), newWorkflowClient(t, server), "Ft0898SEA5N0")
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) {
		t.Fatalf("not an APIError: %v", err)
	}
	return apiErr
}

// Every structured error must carry a hint, and the rejection must reflect the
// real Slack code: a missing/stale trigger is agent-fixable (wrong id), while
// an access denial needs a human.
func TestPreviewWorkflowTriggerRejectionCodes(t *testing.T) {
	notFound := previewRejection(t, "trigger_not_found")
	if notFound.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("trigger_not_found fixable_by = %q, want agent", notFound.FixableBy)
	}
	if !strings.Contains(notFound.Message, "trigger_not_found") {
		t.Errorf("error should surface the real code: %q", notFound.Message)
	}

	denied := previewRejection(t, "trigger_access_denied")
	if denied.FixableBy != agenterrors.FixableByHuman {
		t.Errorf("access-denied fixable_by = %q, want human", denied.FixableBy)
	}
	for _, e := range []*agenterrors.APIError{notFound, denied} {
		if e.Hint == "" || !strings.Contains(e.Hint, "workflow list") {
			t.Errorf("hint should name a recovery command: %q", e.Hint)
		}
	}
}

func TestPreviewWorkflowTriggerNoDataHasHint(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("workflows.triggers.preview", map[string]any{"ok": true, "triggers": []any{}})
	c := newWorkflowClient(t, server)

	_, err := PreviewWorkflowTrigger(context.Background(), c, "Ft0898SEA5N0")
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) || apiErr.Hint == "" {
		t.Errorf("expected an APIError with a hint, got %v", err)
	}
}

func TestGetWorkflowSchemaMissingHasHint(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("workflows.get", map[string]any{"ok": true})
	c := newWorkflowClient(t, server)

	_, err := GetWorkflowSchema(context.Background(), c, "Wf001")
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) || apiErr.Hint == "" {
		t.Errorf("expected an APIError with a hint, got %v", err)
	}
}
