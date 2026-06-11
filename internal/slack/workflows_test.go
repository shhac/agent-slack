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

// Every structured error must carry a hint with the next step — the
// permission-rejection path was returning fixable_by without one.
func TestPreviewWorkflowTriggerRejectedHasHint(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok":                true,
		"triggers":          []any{},
		"rejected_triggers": []any{map[string]any{"id": "Ft0898SEA5N0", "error": "trigger_access_denied"}},
	})
	c := newWorkflowClient(t, server)

	_, err := PreviewWorkflowTrigger(context.Background(), c, "Ft0898SEA5N0")
	if err == nil {
		t.Fatal("expected a rejection error")
	}
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) {
		t.Fatalf("not an APIError: %v", err)
	}
	if apiErr.FixableBy != agenterrors.FixableByHuman {
		t.Errorf("fixable_by = %q, want human", apiErr.FixableBy)
	}
	if apiErr.Hint == "" {
		t.Error("rejection error is missing a hint (AGENTS.md contract)")
	}
	if !strings.Contains(apiErr.Hint, "workflow list") {
		t.Errorf("hint should point at a recovery command: %q", apiErr.Hint)
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
