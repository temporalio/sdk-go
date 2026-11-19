package converter

import (
	"context"

	"go.temporal.io/sdk/internal/common/workflowcontext"
)

// ContextAware is an optional interface that can be implemented alongside
// DataConverter. This interface allows Temporal to pass Workflow/Activity
// contexts to the DataConverter so that it may tailor its behavior.
//
// Exposed as: [go.temporal.io/sdk/converter.ContextAware]
type ContextAware interface {
	WithWorkflowContext(ctx workflowcontext.Context) DataConverter
	WithContext(ctx context.Context) DataConverter
}

// WorkflowContextAwarePayloadCodec lets SDK codec wrappers carry the running
// workflow context to nested deadlock exemptions without binding value converters.
type WorkflowContextAwarePayloadCodec interface {
	WithWorkflowContext(ctx workflowcontext.Context) PayloadCodec
}
