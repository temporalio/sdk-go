package converter

import (
	"context"

	"go.temporal.io/sdk/internal/common/workflowcontext"
)

// WorkflowContext is the workflow execution context.
type WorkflowContext = workflowcontext.Context

// ContextAware is implemented by converters that use workflow or activity context.
type ContextAware interface {
	WithWorkflowContext(ctx WorkflowContext) DataConverter
	WithContext(ctx context.Context) DataConverter
}
