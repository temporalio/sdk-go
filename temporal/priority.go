package temporal

import "go.temporal.io/sdk/internal"

// Priority defines the priority and fairness metadata for activity/workflow.
//
// WARNING: Task queue priority is currently experimental.
type Priority = internal.Priority
