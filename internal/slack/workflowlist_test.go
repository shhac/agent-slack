package slack

import (
	"context"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func workflowListServer(t *testing.T) *mockslack.Server {
	t.Helper()
	server := mockslack.New()
	server.HandleBody("bookmarks.list", map[string]any{
		"ok": true,
		"bookmarks": []any{
			map[string]any{"id": "Bk1", "title": "Live", "shortcut_id": "Ft0LIVEAAAA", "link": "https://slack.com/shortcuts/Ft0LIVEAAAA/a"},
			map[string]any{"id": "Bk2", "title": "Stale", "shortcut_id": "Ft0STALEBBB", "link": "https://slack.com/shortcuts/Ft0STALEBBB/b"},
		},
	})
	server.HandleBody("workflows.featured.list", map[string]any{"ok": false, "error": "unknown_method"})
	server.HandleBody("workflows.triggers.preview", map[string]any{
		"ok": true,
		"triggers": []any{map[string]any{
			"id": "Ft0LIVEAAAA", "name": "Live", "workflow": map[string]any{"workflow_id": "Wf0LIVEAAAA", "title": "Live"},
		}},
		"rejected_triggers": []any{map[string]any{"id": "Ft0STALEBBB", "error": "trigger_not_found"}},
	})
	return server
}

func TestListChannelWorkflowsAnnotatesStaleAndWarmsCache(t *testing.T) {
	server := workflowListServer(t)
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	c := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)

	res, err := ListChannelWorkflows(context.Background(), c, "C0CHANAAAA")
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]ChannelWorkflow{}
	for _, w := range res.Workflows {
		byID[w.TriggerID] = w
	}
	if w := byID["Ft0LIVEAAAA"]; w.Stale {
		t.Errorf("live trigger marked stale: %+v", w)
	}
	if w := byID["Ft0STALEBBB"]; !w.Stale || w.StaleReason != "trigger_not_found" {
		t.Errorf("stale trigger not flagged: %+v", w)
	}

	// One batched preview call validated both triggers.
	if n := len(server.CallsFor("workflows.triggers.preview")); n != 1 {
		t.Fatalf("preview calls = %d, want 1 (batched)", n)
	}

	// The live trigger's preview cache was warmed: a direct preview is free.
	if _, err := PreviewWorkflowTrigger(context.Background(), c, "Ft0LIVEAAAA"); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("workflows.triggers.preview")); n != 1 {
		t.Errorf("preview re-fetched (%d calls); list should have warmed the cache", n)
	}
}

func TestListChannelWorkflowsCachesResult(t *testing.T) {
	server := workflowListServer(t)
	dir := t.TempDir()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

	c1 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	if _, err := ListChannelWorkflows(context.Background(), c1, "C0CHANAAAA"); err != nil {
		t.Fatal(err)
	}

	// A fresh client over the same workspace+dir serves the whole annotated
	// list from cache — zero API calls of any kind.
	before := len(server.Calls())
	c2 := cachingClient(t, server, "https://acme.slack.com", dir, CacheNormal, now)
	res, err := ListChannelWorkflows(context.Background(), c2, "C0CHANAAAA")
	if err != nil {
		t.Fatal(err)
	}
	if len(server.Calls()) != before {
		t.Errorf("cached list made %d API calls, want 0", len(server.Calls())-before)
	}
	if len(res.Workflows) != 2 {
		t.Errorf("cached result lost workflows: %+v", res.Workflows)
	}
}

func TestListChannelWorkflowsBestEffortAnnotation(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("bookmarks.list", map[string]any{
		"ok":        true,
		"bookmarks": []any{map[string]any{"id": "Bk1", "title": "W", "shortcut_id": "Ft0XXXXYYYY", "link": "https://slack.com/shortcuts/Ft0XXXXYYYY/a"}},
	})
	server.HandleBody("workflows.featured.list", map[string]any{"ok": false, "error": "unknown_method"})
	// Preview fails outright — annotation must not fail the list.
	server.HandleBody("workflows.triggers.preview", map[string]any{"ok": false, "error": "not_allowed_token_type"})

	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())
	res, err := ListChannelWorkflows(context.Background(), c, "C0CHANAAAA")
	if err != nil {
		t.Fatalf("annotation failure must not fail the list: %v", err)
	}
	if len(res.Workflows) != 1 || res.Workflows[0].Stale {
		t.Errorf("unvalidated trigger should be returned unmarked: %+v", res.Workflows)
	}
}
