package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// ChannelWorkflow is one workflow discoverable in a channel.
type ChannelWorkflow struct {
	Title     string `json:"title"`
	TriggerID string `json:"trigger_id"`
	Link      string `json:"link,omitempty"`
	AppID     string `json:"app_id,omitempty"`
	Featured  bool   `json:"featured"`
}

type ChannelWorkflows struct {
	ChannelID string            `json:"channel_id"`
	Workflows []ChannelWorkflow `json:"workflows"`
}

var shortcutLinkRe = regexp.MustCompile(`slack\.com/shortcuts/(Ft[A-Za-z0-9]+)`)

// ListChannelWorkflows merges a channel's bookmarked workflows with its
// featured ones (featured adds a flag; bookmarks are the primary source).
func ListChannelWorkflows(ctx context.Context, c *Client, channelID string) (ChannelWorkflows, error) {
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
	return ChannelWorkflows{ChannelID: channelID, Workflows: workflows}, nil
}

func listBookmarkedWorkflows(ctx context.Context, c *Client, channelID string) ([]ChannelWorkflow, error) {
	resp, err := c.API(ctx, "bookmarks.list", map[string]any{"channel_id": channelID})
	if err != nil {
		return nil, err
	}
	var out []ChannelWorkflow
	for _, b := range recItems(getArr(resp, "bookmarks")) {
		link := getStr(b, "link")
		shortcutID := getStr(b, "shortcut_id")
		isWorkflow := shortcutID != "" || shortcutLinkRe.MatchString(link)
		if !isWorkflow {
			continue
		}
		triggerID := shortcutID
		if triggerID == "" {
			if m := shortcutLinkRe.FindStringSubmatch(link); m != nil {
				triggerID = m[1]
			}
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
		TriggerID:   firstNonEmpty(getStr(t, "id"), triggerID),
		Type:        getStr(t, "type"),
		Name:        getStr(t, "name"),
		Description: getStr(t, "description"),
		ShortcutURL: getStr(t, "shortcut_url"),
		Workflow: PreviewWorkflow{
			ID:          getStr(wf, "workflow_id"),
			Title:       getStr(wf, "title"),
			Description: getStr(wf, "description"),
			AppID:       firstNonEmpty(getStr(wf, "app_id"), getStr(wfApp, "id")),
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

func RunWorkflowTrigger(ctx context.Context, c *Client, shortcutURL, channelID, bookmarkID string) (WorkflowRunResult, error) {
	contextJSON, _ := json.Marshal(map[string]string{
		"location":    "bookmark",
		"channel_id":  channelID,
		"bookmark_id": bookmarkID,
	})
	resp, err := c.API(ctx, "workflows.triggers.trip", map[string]any{
		"url":          shortcutURL,
		"client_token": fmt.Sprintf("cli-%d", time.Now().UnixMilli()),
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
		if getStr(b, "shortcut_id") != triggerID {
			continue
		}
		link := getStr(b, "link")
		bookmarkID := getStr(b, "id")
		if link != "" && bookmarkID != "" {
			return ResolvedShortcut{URL: link, BookmarkID: bookmarkID}, nil
		}
	}
	return ResolvedShortcut{}, agenterrors.Newf(agenterrors.FixableByAgent,
		"could not find shortcut URL for trigger %s in channel bookmarks", triggerID).
		WithHint("'agent-slack workflow list <channel>' shows the channel's workflow bookmarks")
}

// FormField is one input of a workflow's open_form step.
type FormField struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Long        bool   `json:"long,omitempty"`
}

// WorkflowSchema is a workflow's definition: form fields + step titles.
type WorkflowSchema struct {
	WorkflowID  string      `json:"workflow_id"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	FormTitle   string      `json:"form_title,omitempty"`
	Fields      []FormField `json:"fields"`
	Steps       []string    `json:"steps"`
}

func GetWorkflowSchema(ctx context.Context, c *Client, workflowID string) (WorkflowSchema, error) {
	if s, ok := c.cachedWorkflowSchema(workflowID); ok {
		return s, nil
	}
	resp, err := c.API(ctx, "workflows.get", map[string]any{"workflow_id": workflowID})
	if err != nil {
		return WorkflowSchema{}, err
	}
	wf := getRec(resp, "workflow")
	if wf == nil {
		return WorkflowSchema{}, agenterrors.Newf(agenterrors.FixableByAgent, "no workflow found for ID %s", workflowID).
			WithHint("check the workflow id (Wf…); 'agent-slack workflow get <Ft-trigger>' resolves a trigger to its workflow")
	}

	schema := assembleWorkflowSchema(wf, workflowID)
	c.cacheWorkflowSchema(workflowID, schema)
	return schema, nil
}

// assembleWorkflowSchema shapes a raw workflows.get workflow object into the
// schema: step titles plus the open_form step's fields. Pure.
func assembleWorkflowSchema(wf map[string]any, workflowID string) WorkflowSchema {
	schema := WorkflowSchema{
		WorkflowID:  firstNonEmpty(getStr(wf, "id"), workflowID),
		Title:       getStr(wf, "title"),
		Description: getStr(wf, "description"),
		Fields:      []FormField{},
		Steps:       []string{},
	}
	for _, step := range recItems(getArr(wf, "steps")) {
		fn := getRec(step, "function")
		callbackID := getStr(fn, "callback_id")
		schema.Steps = append(schema.Steps, firstNonEmpty(getStr(fn, "title"), callbackID))

		if callbackID != "open_form" {
			continue
		}
		inputs := getRec(step, "inputs")
		schema.FormTitle = getStr(getRec(inputs, "title"), "value")
		schema.Fields = append(schema.Fields, extractFormFields(getRec(getRec(inputs, "fields"), "value"))...)
	}
	return schema
}

// extractFormFields shapes an open_form step's fields.value object — the
// element list plus its sibling required-names array — into FormFields.
func extractFormFields(fieldsValue map[string]any) []FormField {
	required := map[string]bool{}
	for _, r := range getArr(fieldsValue, "required") {
		if s, ok := r.(string); ok && s != "" {
			required[s] = true
		}
	}
	var fields []FormField
	for _, el := range recItems(getArr(fieldsValue, "elements")) {
		name := getStr(el, "name")
		fieldType := getStr(el, "type")
		if fieldType == "" {
			fieldType = "string"
		}
		fields = append(fields, FormField{
			Name:        name,
			Title:       getStr(el, "title"),
			Type:        fieldType,
			Description: getStr(el, "description"),
			Required:    required[name],
			Long:        getBool(el, "long"),
		})
	}
	return fields
}
