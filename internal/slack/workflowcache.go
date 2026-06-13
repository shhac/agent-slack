package slack

// Workflow preview (Ft… → workflow id + shortcut) and schema (Wf… → form
// fields) are pure reads keyed by one ID — good cache candidates. Only
// successful results are cached: a rejection (trigger_not_found, access
// denied) must never stick for the TTL, and the side-effecting `workflow run`
// bookmark resolution (ResolveShortcut) is deliberately left uncached.

// The same validator guards both directions of each pair, so an entry that
// would be pruned on the next load is never written in the first place.
func validWorkflowPreview(_ string, p WorkflowPreview) bool {
	return p.TriggerID != "" || p.Workflow.ID != ""
}

func validWorkflowSchema(_ string, s WorkflowSchema) bool { return s.WorkflowID != "" }

func (c *Client) workflowPreviewCache() *cacheSnapshot[WorkflowPreview] {
	return openCache(c.cache, "workflow-triggers", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).WorkflowPreview, validWorkflowPreview)
}

func (c *Client) workflowSchemaCache() *cacheSnapshot[WorkflowSchema] {
	return openCache(c.cache, "workflow-schemas", c.currentAuth().WorkspaceURL,
		cacheTTLOf(c.cache).WorkflowSchema, validWorkflowSchema)
}

func (c *Client) cachedWorkflowPreview(triggerID string) (WorkflowPreview, bool) {
	return c.workflowPreviewCache().get(triggerID)
}

func (c *Client) cacheWorkflowPreview(triggerID string, p WorkflowPreview) {
	if !validWorkflowPreview(triggerID, p) {
		return
	}
	snap := c.workflowPreviewCache()
	snap.set(triggerID, p)
	snap.save()
}

func (c *Client) cachedWorkflowSchema(workflowID string) (WorkflowSchema, bool) {
	return c.workflowSchemaCache().get(workflowID)
}

func (c *Client) cacheWorkflowSchema(workflowID string, s WorkflowSchema) {
	if !validWorkflowSchema(workflowID, s) {
		return
	}
	snap := c.workflowSchemaCache()
	snap.set(workflowID, s)
	snap.save()
}
