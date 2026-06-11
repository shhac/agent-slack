package slack

// Workflow preview (Ft… → workflow id + shortcut) and schema (Wf… → form
// fields) are pure reads keyed by one ID — good cache candidates. Only
// successful results are cached: a rejection (trigger_not_found, access
// denied) must never stick for the TTL, and the side-effecting `workflow run`
// bookmark resolution (ResolveShortcut) is deliberately left uncached.

func (c *Client) cachedWorkflowPreview(triggerID string) (WorkflowPreview, bool) {
	snap := openCache[WorkflowPreview](c.cache, "workflow-triggers", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).WorkflowPreview,
		func(_ string, p WorkflowPreview) bool { return p.TriggerID != "" || p.Workflow.ID != "" })
	return snap.get(triggerID)
}

func (c *Client) cacheWorkflowPreview(triggerID string, p WorkflowPreview) {
	if triggerID == "" {
		return
	}
	snap := openCache[WorkflowPreview](c.cache, "workflow-triggers", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).WorkflowPreview, nil)
	snap.set(triggerID, p)
	snap.save()
}

func (c *Client) cachedWorkflowSchema(workflowID string) (WorkflowSchema, bool) {
	snap := openCache[WorkflowSchema](c.cache, "workflow-schemas", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).WorkflowSchema,
		func(_ string, s WorkflowSchema) bool { return s.WorkflowID != "" })
	return snap.get(workflowID)
}

func (c *Client) cacheWorkflowSchema(workflowID string, s WorkflowSchema) {
	if workflowID == "" {
		return
	}
	snap := openCache[WorkflowSchema](c.cache, "workflow-schemas", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).WorkflowSchema, nil)
	snap.set(workflowID, s)
	snap.save()
}
