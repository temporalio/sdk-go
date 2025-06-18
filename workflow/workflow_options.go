package workflow

import (
	"time"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/temporal"
)

// WithChildOptions adds all workflow options to the context.
func WithChildOptions(ctx Context, cwo ChildWorkflowOptions) Context {
	return internal.WithChildWorkflowOptions(ctx, cwo)
}

// WithWorkflowNamespace adds a namespace to the context.
func WithWorkflowNamespace(ctx Context, name string) Context {
	return internal.WithWorkflowNamespace(ctx, name)
}

// WithWorkflowTaskQueue adds a task queue to the context.
func WithWorkflowTaskQueue(ctx Context, name string) Context {
	return internal.WithWorkflowTaskQueue(ctx, name)
}

// WithWorkflowID adds a workflowID to the context.
func WithWorkflowID(ctx Context, workflowID string) Context {
	return internal.WithWorkflowID(ctx, workflowID)
}

// WithWorkflowRunTimeout adds a run timeout to the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithWorkflowRunTimeout(ctx Context, d time.Duration) Context {
	return internal.WithWorkflowRunTimeout(ctx, d)
}

// WithWorkflowTaskTimeout adds a workflow task timeout to the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithWorkflowTaskTimeout(ctx Context, d time.Duration) Context {
	return internal.WithWorkflowTaskTimeout(ctx, d)
}

// WithDataConverter adds DataConverter to the context.
func WithDataConverter(ctx Context, dc converter.DataConverter) Context {
	return internal.WithDataConverter(ctx, dc)
}

// WithWorkflowPriority adds a priority to the context.
//
// WARNING: Task queue priority is currently experimental.
func WithWorkflowPriority(ctx Context, priority internal.Priority) Context {
	return internal.WithWorkflowPriority(ctx, priority)
}

// GetChildWorkflowOptions returns all workflow options present on the context.
func GetChildWorkflowOptions(ctx Context) ChildWorkflowOptions {
	return internal.GetChildWorkflowOptions(ctx)
}

// WithWorkflowVersioningIntent is used to set the VersioningIntent before constructing a
// ContinueAsNewError with NewContinueAsNewError.
//
// Deprecated: Build-id based versioning is deprecated in favor of worker deployment based versioning and will be removed soon.
func WithWorkflowVersioningIntent(ctx Context, intent temporal.VersioningIntent) Context {
	return internal.WithWorkflowVersioningIntent(ctx, intent)
}
