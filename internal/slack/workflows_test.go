package slack

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/mockslack"
)

func newWorkflowClient(t *testing.T, server *mockslack.Server) *Client {
	t.Helper()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return New(Auth{Type: AuthStandard, Token: "xoxb-test", WorkspaceURL: ts.URL}, WithBaseURL(ts.URL))
}

// A bookmark whose trigger id is only derivable from its shortcut link (no
// shortcut_id field) is listed by ListChannelWorkflows, so ResolveShortcut
// must resolve it too — list and run share bookmarkTrigger for exactly this.
func TestResolveShortcutLinkOnlyBookmark(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("bookmarks.list", map[string]any{
		"ok": true,
		"bookmarks": []any{
			map[string]any{"id": "Bk0", "title": "Docs", "link": "https://example.com/not-a-workflow"},
			map[string]any{"id": "Bk1", "title": "Link only", "link": "https://slack.com/shortcuts/Ft0LINKONLY1/abc"},
		},
	})
	got, err := ResolveShortcut(context.Background(), newWorkflowClient(t, server), "C1", "Ft0LINKONLY1")
	if err != nil {
		t.Fatal(err)
	}
	if got.BookmarkID != "Bk1" || !strings.Contains(got.URL, "Ft0LINKONLY1") {
		t.Errorf("got = %+v", got)
	}
}

func previewRejection(t *testing.T, code string) *agenterrors.APIError {
	t.Helper()
	server := mockslack.New()
	server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok":                true,
		"triggers":          []any{},
		"rejected_triggers": []any{map[string]any{"id": "Ft0123ABCDEF", "error": code}},
	})
	_, err := PreviewWorkflowTrigger(context.Background(), newWorkflowClient(t, server), "Ft0123ABCDEF")
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

	_, err := PreviewWorkflowTrigger(context.Background(), c, "Ft0123ABCDEF")
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) || apiErr.Hint == "" {
		t.Errorf("expected an APIError with a hint, got %v", err)
	}
}

func TestPreviewWorkflowTriggerCachesSuccessOnly(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok": true,
		"triggers": []any{map[string]any{
			"id": "Ft0123ABCDEF", "name": "PRs", "shortcut_url": "https://slack.com/shortcuts/Ft0123ABCDEF/x",
			"workflow": map[string]any{"workflow_id": "Wf0123ABCDEF", "title": "PRs"},
		}},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if p, err := PreviewWorkflowTrigger(context.Background(), c, "Ft0123ABCDEF"); err != nil || p.Workflow.ID != "Wf0123ABCDEF" {
		t.Fatalf("preview = %+v, %v", p, err)
	}

	server.Reset()
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if p, err := PreviewWorkflowTrigger(context.Background(), c2, "Ft0123ABCDEF"); err != nil || p.Workflow.ID != "Wf0123ABCDEF" {
		t.Errorf("cached preview = %+v, %v", p, err)
	}
	if calls := len(server.CallsFor("workflows.triggers.preview")); calls != 0 {
		t.Errorf("expected preview served from cache, got %d calls", calls)
	}
}

func TestPreviewWorkflowTriggerDoesNotCacheRejection(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok": true, "triggers": []any{},
		"rejected_triggers": []any{map[string]any{"id": "Ft0123ABCDEF", "error": "trigger_not_found"}},
	})
	dir := t.TempDir()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if _, err := PreviewWorkflowTrigger(context.Background(), c, "Ft0123ABCDEF"); err == nil {
		t.Fatal("expected rejection error")
	}
	// A second attempt must hit the API again — rejections are never cached.
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if _, err := PreviewWorkflowTrigger(context.Background(), c2, "Ft0123ABCDEF"); err == nil {
		t.Fatal("expected rejection error")
	}
	if calls := len(server.CallsFor("workflows.triggers.preview")); calls != 2 {
		t.Errorf("rejection must not be cached; preview calls = %d, want 2", calls)
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
