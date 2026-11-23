package temporalnexus

import (
	"context"
	"errors"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// DynamicTemporalOperationOptions configures an unnamed Temporal-backed fallback.
// Start receives input as converter.RawValue, with payload codecs already applied,
// and the original operation name through nexus.ExtractHandlerInfo(ctx).
// Results use any because different operation names may return different types.
// Handlers may run concurrently.
//
// NOTE: Experimental
type DynamicTemporalOperationOptions struct {
	Start func(context.Context, NexusClient, converter.RawValue, StartTemporalOperationOptions) (TemporalOperationResult[any], error)
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
		Start: func(ctx context.Context, nc NexusClient, input *nexus.LazyValue, startOpts StartTemporalOperationOptions) (TemporalOperationResult[any], error) {
			var raw converter.RawValue
			if err := input.Consume(&raw); err != nil {
				return TemporalOperationResult[any]{}, err
			}
			return opts.Start(ctx, nc, raw, startOpts)
		},
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
