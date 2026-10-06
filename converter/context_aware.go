package converter

import (
	"context"

	"go.temporal.io/sdk/internal/common/workflowcontext"
)

// ContextAware is an optional interface that can be implemented alongside
// DataConverter. This interface allows Temporal to pass Workflow/Activity
// contexts to the DataConverter so that it may tailor its behavior.
//
// Note that data converters may be called in non-context-aware situations to
// convert payloads that may not be customized per context. Data converter
// implementers should not expect or require contextual data be present.
type ContextAware interface {
	WithWorkflowContext(ctx workflowcontext.Context) DataConverter
	WithContext(ctx context.Context) DataConverter
}
