package workflow

import (
	"time"

	"go.temporal.io/temporal/encoded"
	"go.temporal.io/temporal/internal"
)

// WithChildOptions adds all workflow options to the context.
func WithChildOptions(ctx Context, cwo ChildWorkflowOptions) Context {
	return internal.WithChildWorkflowOptions(ctx, cwo)
}

// WithWorkflowNamespace adds a namespace to the context.
func WithWorkflowNamespace(ctx Context, name string) Context {
	return internal.WithWorkflowNamespace(ctx, name)
}

// WithWorkflowTaskList adds a task list to the context.
func WithWorkflowTaskList(ctx Context, name string) Context {
	return internal.WithWorkflowTaskList(ctx, name)
}

// WithWorkflowID adds a workflowID to the context.
func WithWorkflowID(ctx Context, workflowID string) Context {
	return internal.WithWorkflowID(ctx, workflowID)
}

// WithExecutionStartToCloseTimeout adds a workflow execution timeout to the context.
func WithExecutionStartToCloseTimeout(ctx Context, d time.Duration) Context {
	return internal.WithExecutionStartToCloseTimeout(ctx, d)
}

// WithWorkflowTaskStartToCloseTimeout adds a decision timeout to the context.
func WithWorkflowTaskStartToCloseTimeout(ctx Context, d time.Duration) Context {
	return internal.WithWorkflowTaskStartToCloseTimeout(ctx, d)
}

// WithDataConverter adds DataConverter to the context.
func WithDataConverter(ctx Context, dc encoded.DataConverter) Context {
	return internal.WithDataConverter(ctx, dc)
}
