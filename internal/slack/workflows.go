package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// ChannelWorkflow is one workflow discoverable in a channel. Stale marks a
// trigger that is listed (a lingering bookmark) but cannot be previewed —
// usually because the workflow was deleted (trigger_not_found) or is not
// shared with this user; StaleReason carries the Slack code.
type ChannelWorkflow struct {
	Title       string `json:"title"`
	TriggerID   string `json:"trigger_id"`
	Link        string `json:"link,omitempty"`
	AppID       string `json:"app_id,omitempty"`
	Featured    bool   `json:"featured"`
	Stale       bool   `json:"stale,omitempty"`
	StaleReason string `json:"stale_reason,omitempty"`
}

type ChannelWorkflows struct {
	ChannelID string            `json:"channel_id"`
	Workflows []ChannelWorkflow `json:"workflows"`
}

var shortcutLinkRe = regexp.MustCompile(`slack\.com/shortcuts/(Ft[A-Za-z0-9]+)`)

// ListChannelWorkflows merges a channel's bookmarked workflows with its
// featured ones (featured adds a flag; bookmarks are the primary source), then
// validates every listed trigger in one batched preview call so stale
// bookmarks are flagged and the per-trigger preview cache is warmed. The whole
// annotated result is cached per channel.
func ListChannelWorkflows(ctx context.Context, c *Client, channelID string) (ChannelWorkflows, error) {
	if cached, ok := c.cachedWorkflowList(channelID); ok {
		return cached, nil
	}

	bookmarked, err := listBookmarkedWorkflows(ctx, c, channelID)
	if err != nil {
		return ChannelWorkflows{}, err
	}
	featured := listFeaturedWorkflows(ctx, c, channelID) // best effort

	featuredIDs := map[string]bool{}
	for _, f := range featured {
		featuredIDs[f.TriggerID] = true
	}
	seen := map[string]bool{}
	workflows := []ChannelWorkflow{}
	for _, bk := range bookmarked {
		if bk.TriggerID != "" {
			seen[bk.TriggerID] = true
		}
		bk.Featured = featuredIDs[bk.TriggerID]
		workflows = append(workflows, bk)
	}
	for _, ft := range featured {
		if !seen[ft.TriggerID] {
			workflows = append(workflows, ChannelWorkflow{Title: ft.Title, TriggerID: ft.TriggerID, Featured: true})
		}
	}

	result := ChannelWorkflows{ChannelID: channelID, Workflows: workflows}
	annotateStaleTriggers(ctx, c, &result)
	c.cacheWorkflowList(channelID, result)
	return result, nil
}

// annotateStaleTriggers validates every listed trigger in a single batched
// workflows.triggers.preview call: valid triggers warm the per-trigger preview
// cache (so a later `workflow preview/get` is free), rejected ones are marked
// Stale with the Slack reason. Best-effort — if the batch call fails (e.g. the
// token cannot preview), the list is returned unannotated rather than erroring.
func annotateStaleTriggers(ctx context.Context, c *Client, result *ChannelWorkflows) {
	var ids []string
	seen := map[string]bool{}
	for _, w := range result.Workflows {
		if w.TriggerID != "" && !seen[w.TriggerID] {
			seen[w.TriggerID] = true
			ids = append(ids, w.TriggerID)
		}
	}
	if len(ids) == 0 {
		return
	}

	resp, err := c.API(ctx, "workflows.triggers.preview", map[string]any{"trigger_ids": strings.Join(ids, ",")})
	if err != nil {
		return // best-effort: annotation is a bonus, never fails the list
	}

	for _, t := range recItems(getArr(resp, "triggers")) {
		if id := getStr(t, "id"); id != "" {
			c.cacheWorkflowPreview(id, assembleWorkflowPreview(t, id))
		}
	}

	rejected := map[string]string{}
	for _, r := range recItems(getArr(resp, "rejected_triggers")) {
		if id := getStr(r, "id"); id != "" {
			rejected[id] = getStr(r, "error")
		}
	}
	for i := range result.Workflows {
		if reason, bad := rejected[result.Workflows[i].TriggerID]; bad {
			result.Workflows[i].Stale = true
			result.Workflows[i].StaleReason = reason
		}
	}
}

// bookmarkTrigger extracts the workflow-trigger identity of one bookmark:
// shortcut_id when present, else the Ft… id parsed from a shortcut link. An
// empty triggerID means the bookmark is not a workflow shortcut. The one
// place this rule lives — list and run must agree on it, or a bookmark could
// be listed but not runnable.
func bookmarkTrigger(b map[string]any) (triggerID, link, bookmarkID string) {
	link = getStr(b, "link")
	bookmarkID = getStr(b, "id")
	triggerID = getStr(b, "shortcut_id")
	if triggerID == "" {
		if m := shortcutLinkRe.FindStringSubmatch(link); m != nil {
			triggerID = m[1]
		}
	}
	return triggerID, link, bookmarkID
}

func listBookmarkedWorkflows(ctx context.Context, c *Client, channelID string) ([]ChannelWorkflow, error) {
	resp, err := c.API(ctx, "bookmarks.list", map[string]any{"channel_id": channelID})
	if err != nil {
		return nil, err
	}
	var out []ChannelWorkflow
	for _, b := range recItems(getArr(resp, "bookmarks")) {
		triggerID, link, _ := bookmarkTrigger(b)
		if triggerID == "" {
			continue
		}
		out = append(out, ChannelWorkflow{
			Title:     getStr(b, "title"),
			TriggerID: triggerID,
			Link:      link,
			AppID:     getStr(b, "app_id"),
		})
	}
	return out, nil
}

func listFeaturedWorkflows(ctx context.Context, c *Client, channelID string) []ChannelWorkflow {
	channelIDs, _ := json.Marshal([]string{channelID})
	resp, err := c.API(ctx, "workflows.featured.list", map[string]any{"channel_ids": string(channelIDs)})
	if err != nil {
		return nil // workflows.featured.list may not be available — not fatal
	}
	for _, entry := range recItems(getArr(resp, "featured_workflows")) {
		if getStr(entry, "channel_id") != channelID {
			continue
		}
		var out []ChannelWorkflow
		for _, t := range recItems(getArr(entry, "triggers")) {
			if id := getStr(t, "id"); id != "" {
				out = append(out, ChannelWorkflow{TriggerID: id, Title: getStr(t, "title")})
			}
		}
		return out
	}
	return nil
}

// WorkflowPreview is workflow metadata from a trigger ID (no side effects).
type WorkflowPreview struct {
	TriggerID     string          `json:"trigger_id"`
	Type          string          `json:"type"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	ShortcutURL   string          `json:"shortcut_url,omitempty"`
	Workflow      PreviewWorkflow `json:"workflow"`
	Collaborators []string        `json:"collaborators,omitempty"`
}

type PreviewWorkflow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	AppID       string `json:"app_id"`
	AppName     string `json:"app_name,omitempty"`
}

// rejectedTriggerError maps a workflows.triggers.preview rejection code to a
// typed error. Slack reports the real reason (e.g. trigger_not_found vs an
// access denial) inside rejected_triggers[].error; the code decides whether an
// agent can fix it (wrong/stale id) or a human must (sharing/permissions).
func rejectedTriggerError(triggerID, code string) error {
	switch code {
	case "trigger_not_found", "trigger_does_not_exist", "not_found":
		return agenterrors.Newf(agenterrors.FixableByAgent,
			"trigger %s was rejected: %s", triggerID, code).
			WithHint("the trigger id (Ft…) is wrong or stale; 'agent-slack workflow list <channel>' lists the triggers currently in a channel")
	case "":
		return agenterrors.Newf(agenterrors.FixableByHuman,
			"trigger %s was rejected", triggerID).
			WithHint("a workflow collaborator may need to share it with you; or verify the id with 'agent-slack workflow list <channel>'")
	default:
		return agenterrors.Newf(agenterrors.FixableByHuman,
			"trigger %s was rejected: %s", triggerID, code).
			WithHint("if this is a permissions error, ask a workflow collaborator to share it; otherwise verify the trigger id with 'agent-slack workflow list <channel>'")
	}
}

func PreviewWorkflowTrigger(ctx context.Context, c *Client, triggerID string) (WorkflowPreview, error) {
	if p, ok := c.cachedWorkflowPreview(triggerID); ok {
		return p, nil
	}
	resp, err := c.API(ctx, "workflows.triggers.preview", map[string]any{"trigger_ids": triggerID})
	if err != nil {
		return WorkflowPreview{}, err
	}
	triggers := recItems(getArr(resp, "triggers"))
	if len(triggers) == 0 {
		if rejected := recItems(getArr(resp, "rejected_triggers")); len(rejected) > 0 {
			return WorkflowPreview{}, rejectedTriggerError(triggerID, getStr(rejected[0], "error"))
		}
		return WorkflowPreview{}, agenterrors.Newf(agenterrors.FixableByAgent,
			"no preview data returned for trigger %s", triggerID).
			WithHint("check the trigger id (Ft…); 'agent-slack workflow list <channel>' lists the triggers in a channel")
	}
	preview := assembleWorkflowPreview(triggers[0], triggerID)
	c.cacheWorkflowPreview(triggerID, preview)
	return preview, nil
}

// assembleWorkflowPreview shapes one raw workflows.triggers.preview trigger
// object. Pure: testable against captured API payloads without a client.
func assembleWorkflowPreview(t map[string]any, triggerID string) WorkflowPreview {
	wf := getRec(t, "workflow")
	wfApp := getRec(wf, "app")
	details := getRec(t, "workflow_details")

	var collaborators []string
	for _, col := range getArr(details, "collaborators") {
		if s, ok := col.(string); ok && s != "" {
			collaborators = append(collaborators, s)
		}
	}
	return WorkflowPreview{
		TriggerID:   FirstNonEmpty(getStr(t, "id"), triggerID),
		Type:        getStr(t, "type"),
		Name:        getStr(t, "name"),
		Description: getStr(t, "description"),
		ShortcutURL: getStr(t, "shortcut_url"),
		Workflow: PreviewWorkflow{
			ID:          getStr(wf, "workflow_id"),
			Title:       getStr(wf, "title"),
			Description: getStr(wf, "description"),
			AppID:       FirstNonEmpty(getStr(wf, "app_id"), getStr(wfApp, "id")),
			AppName:     getStr(wfApp, "name"),
		},
		Collaborators: collaborators,
	}
}

// WorkflowRunResult is the outcome of tripping a trigger.
type WorkflowRunResult struct {
	FunctionExecutionID string `json:"function_execution_id,omitempty"`
	TriggerExecutionID  string `json:"trigger_execution_id,omitempty"`
	IsSlowWorkflow      bool   `json:"is_slow_workflow,omitempty"`
}

// clientToken mints the client-token format both workflows.triggers.trip and
// views.submit send. The real client mints a fresh token per call too — the
// values do not correlate the flow.
func clientToken() string {
	return fmt.Sprintf("cli-%d", time.Now().UnixMilli())
}

func RunWorkflowTrigger(ctx context.Context, c *Client, shortcutURL, channelID, bookmarkID string) (WorkflowRunResult, error) {
	contextJSON, _ := json.Marshal(map[string]string{
		"location":    "bookmark",
		"channel_id":  channelID,
		"bookmark_id": bookmarkID,
	})
	resp, err := c.API(ctx, "workflows.triggers.trip", map[string]any{
		"url":          shortcutURL,
		"client_token": clientToken(),
		"context":      string(contextJSON),
		"run_precheck": true,
	})
	if err != nil {
		return WorkflowRunResult{}, err
	}
	return WorkflowRunResult{
		FunctionExecutionID: getStr(resp, "function_execution_id"),
		TriggerExecutionID:  getStr(resp, "trigger_execution_id"),
		IsSlowWorkflow:      getBool(resp, "is_slow_workflow"),
	}, nil
}

// ResolvedShortcut is the bookmark carrying a trigger's shortcut URL.
type ResolvedShortcut struct {
	URL        string
	BookmarkID string
}

// ResolveShortcut finds the bookmark whose shortcut matches the trigger —
// tripping needs the bookmark's URL + id for context.
func ResolveShortcut(ctx context.Context, c *Client, channelID, triggerID string) (ResolvedShortcut, error) {
	resp, err := c.API(ctx, "bookmarks.list", map[string]any{"channel_id": channelID})
	if err != nil {
		return ResolvedShortcut{}, err
	}
	for _, b := range recItems(getArr(resp, "bookmarks")) {
		id, link, bookmarkID := bookmarkTrigger(b)
		if id != "" && id == triggerID && link != "" && bookmarkID != "" {
			return ResolvedShortcut{URL: link, BookmarkID: bookmarkID}, nil
		}
	}
	return ResolvedShortcut{}, agenterrors.Newf(agenterrors.FixableByAgent,
		"could not find shortcut URL for trigger %s in channel bookmarks", triggerID).
		WithHint("'agent-slack workflow list <channel>' shows the channel's workflow bookmarks")
}

// Trigger preview and workflow schema (read-only introspection) live in
// workflows_schema.go.
