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

func validWorkflowList(_ string, w ChannelWorkflows) bool { return w.ChannelID != "" }

func (c *Client) workflowListCache() *cacheSnapshot[ChannelWorkflows] {
	return openCacheFor(c, "workflow-list", cacheTTLOf(c.cache).WorkflowList, validWorkflowList)
}

func (c *Client) cachedWorkflowList(channelID string) (ChannelWorkflows, bool) {
	return c.workflowListCache().get(channelID)
}

func (c *Client) cacheWorkflowList(channelID string, w ChannelWorkflows) {
	cacheSet(c.workflowListCache(), channelID, w, channelID != "" && validWorkflowList(channelID, w))
}

func (c *Client) workflowPreviewCache() *cacheSnapshot[WorkflowPreview] {
	return openCacheFor(c, cacheCategoryWorkflowTriggers, cacheTTLOf(c.cache).WorkflowPreview, validWorkflowPreview)
}

func (c *Client) workflowSchemaCache() *cacheSnapshot[WorkflowSchema] {
	return openCacheFor(c, "workflow-schemas", cacheTTLOf(c.cache).WorkflowSchema, validWorkflowSchema)
}

func (c *Client) cachedWorkflowPreview(triggerID string) (WorkflowPreview, bool) {
	return c.workflowPreviewCache().get(triggerID)
}

func (c *Client) cacheWorkflowPreview(triggerID string, p WorkflowPreview) {
	cacheSet(c.workflowPreviewCache(), triggerID, p, validWorkflowPreview(triggerID, p))
}

func (c *Client) cachedWorkflowSchema(workflowID string) (WorkflowSchema, bool) {
	return c.workflowSchemaCache().get(workflowID)
}

func (c *Client) cacheWorkflowSchema(workflowID string, s WorkflowSchema) {
	cacheSet(c.workflowSchemaCache(), workflowID, s, validWorkflowSchema(workflowID, s))
}
