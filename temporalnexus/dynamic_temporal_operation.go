package temporalnexus

import (
	"context"
	"errors"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
)

// DynamicTemporalOperationOptions configures an unnamed Temporal-backed fallback.
// Start receives raw input and the original operation name through
// nexus.ExtractHandlerInfo(ctx). It must consume the input before returning.
// Results use any because different operation names may return different types.
// Handlers may run concurrently.
//
// NOTE: Experimental
type DynamicTemporalOperationOptions struct {
	Start func(context.Context, NexusClient, *nexus.LazyValue, StartTemporalOperationOptions) (TemporalOperationResult[any], error)
	// Nil cancellation handlers use the same defaults as TemporalOperationOptions.
	CancelWorkflowRun       func(context.Context, client.Client, CancelTemporalWorkflowRunOptions, nexus.CancelOperationOptions) error
	CancelWorkflowUpdate    func(context.Context, client.Client, CancelTemporalUpdateWorkflowOptions, nexus.CancelOperationOptions) error
	CancelActivityExecution func(context.Context, client.Client, CancelTemporalActivityExecutionOptions, nexus.CancelOperationOptions) error
}

// NewDynamicTemporalOperation creates a handler for Service.RegisterDynamic.
// Named handlers take precedence. Unmatched starts and cancellations reach this handler.
//
// NOTE: Experimental
func NewDynamicTemporalOperation(opts DynamicTemporalOperationOptions) (nexus.OperationHandler[*nexus.LazyValue, any], error) {
	if opts.Start == nil {
		return nil, errors.New("invalid dynamic operation options: Start is required")
	}
	options := TemporalOperationOptions[*nexus.LazyValue, any]{
		Start:                   opts.Start,
		CancelWorkflowRun:       opts.CancelWorkflowRun,
		CancelWorkflowUpdate:    opts.CancelWorkflowUpdate,
		CancelActivityExecution: opts.CancelActivityExecution,
	}
	if options.CancelWorkflowRun == nil {
		options.CancelWorkflowRun = defaultCancelWorkflowRun
	}
	if options.CancelWorkflowUpdate == nil {
		options.CancelWorkflowUpdate = defaultCancelWorkflowUpdate
	}
	if options.CancelActivityExecution == nil {
		options.CancelActivityExecution = defaultCancelActivityExecution
	}
	return &temporalOperation[*nexus.LazyValue, any]{options: options}, nil
}

// MustNewDynamicTemporalOperation creates an unnamed Temporal-backed fallback.
// It panics if the options are invalid.
//
// NOTE: Experimental
func MustNewDynamicTemporalOperation(opts DynamicTemporalOperationOptions) nexus.OperationHandler[*nexus.LazyValue, any] {
	handler, err := NewDynamicTemporalOperation(opts)
	if err != nil {
		panic(err)
	}
	return handler
}
