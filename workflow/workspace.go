package workflow

// GetWorkspaceID returns a run-scoped workspace ID in the format "{runID}/{name}".
// If name is empty, it defaults to "default", producing "{runID}/default".
// This generates a workspace ID that is guaranteed unique within the namespace
// since run IDs are UUIDs.
//
// Usage:
//
//	// Common case: one workspace per run
//	wsID := workflow.GetWorkspaceID(ctx, "")  // "{runID}/default"
//
//	// Multiple workspaces per run
//	codeWsID := workflow.GetWorkspaceID(ctx, "code")  // "{runID}/code"
//	dataWsID := workflow.GetWorkspaceID(ctx, "data")  // "{runID}/data"
func GetWorkspaceID(ctx Context, name string) string {
	if name == "" {
		name = "default"
	}
	info := GetInfo(ctx)
	return info.WorkflowExecution.RunID + "/" + name
}
