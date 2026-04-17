package temporalnexus

import (
	"context"
	"errors"
	"strings"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/workflow"
)

// TemporalOperationResult encapsulates either a synchronous result or an asynchronous operation token.
// Exactly one of sync or token must be set.
type TemporalOperationResult[R any] struct {
	sync  *R
	token string
}

// NewSyncResult creates a TemporalOperationResult with a synchronous value.
func NewSyncResult[R any](value R) TemporalOperationResult[R] {
	return TemporalOperationResult[R]{sync: &value}
}

// NewAsyncResult creates a TemporalOperationResult with an asynchronous operation token.
func NewAsyncResult[R any](token string) TemporalOperationResult[R] {
	return TemporalOperationResult[R]{token: token}
}

// toHandlerResult converts this result to a nexus.HandlerStartOperationResult.
// Returns an error if both sync and token are set, or neither is set.
func (r TemporalOperationResult[R]) toHandlerResult() (nexus.HandlerStartOperationResult[R], error) {
	hasSync := r.sync != nil
	hasToken := r.token != ""

	if hasSync && hasToken {
		return nil, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal, "invalid TemporalOperationResult: both sync and token are set")
	}
	if !hasSync && !hasToken {
		return nil, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal, "invalid TemporalOperationResult: neither sync nor token is set")
	}

	if hasSync {
		return &nexus.HandlerStartOperationResultSync[R]{Value: *r.sync}, nil
	}
	return &nexus.HandlerStartOperationResultAsync{OperationToken: r.token}, nil
}

// NexusClient wraps the Temporal client.Client with Nexus-aware context.
// It is scoped to a single operation invocation and should not be stored beyond the Start callback.
type NexusClient struct {
	client                client.Client
	namespace             string
	taskQueue             string
	startOperationOptions nexus.StartOperationOptions
	asyncStarted          *bool
}

// GetWorkflowClient returns the underlying Temporal client for advanced or fallback use cases.
func (nc NexusClient) GetWorkflowClient() client.Client {
	return nc.client
}

// StartWorkflow starts a type-safe workflow run for a Nexus operation, linking the execution
// chain to the Nexus operation (callbacks, links, request ID). It always returns an async
// result with a workflow-run operation token.
//
// The workflow parameter must have the signature func(workflow.Context, I) (O, error).
// For workflows that don't follow this signature, use [StartUntypedWorkflow].
//
// These are free functions because Go does not allow generic methods on non-generic structs.
func StartWorkflow[I, O any, WF func(workflow.Context, I) (O, error)](
	ctx context.Context,
	nc NexusClient,
	workflowOpts client.StartWorkflowOptions,
	workflow WF,
	arg I,
) (TemporalOperationResult[O], error) {
	return StartUntypedWorkflow[O](ctx, nc, workflowOpts, workflow, arg)
}

// StartUntypedWorkflow starts a workflow run for a Nexus operation by function reference or
// string name, linking the execution chain to the Nexus operation (callbacks, links, request ID).
// It always returns an async result with a workflow-run operation token.
//
// Useful for invoking workflows that don't follow the single argument / single return type signature.
// See [StartWorkflow] for the type-safe variant.
func StartUntypedWorkflow[R any](
	ctx context.Context,
	nc NexusClient,
	workflowOpts client.StartWorkflowOptions,
	workflow any,
	args ...any,
) (TemporalOperationResult[R], error) {
	if nc.asyncStarted != nil && *nc.asyncStarted {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "only one async operation can be started per operation invocation")
	}

	// Prevent the test env client from panicking when we try to use it from an operation.
	ctx = context.WithValue(ctx, internal.IsWorkflowRunOpContextKey, true)

	handle, err := ExecuteUntypedWorkflow[R](ctx, nc.startOperationOptions, workflowOpts, workflow, args...)
	if err != nil {
		return TemporalOperationResult[R]{}, err
	}

	if nc.asyncStarted != nil {
		*nc.asyncStarted = true
	}
	nexus.AddHandlerLinks(ctx, handle.link())
	return NewAsyncResult[R](handle.token()), nil
}

// TemporalOperationOptions configures a generic Temporal Nexus operation.
//
// Asynchronous workflow-backed operation:
//
//	op, err := NewTemporalOperation(TemporalOperationOptions[MyInput, MyOutput]{
//		Name: "my-async-op",
//		Start: func(ctx context.Context, nc NexusClient, input MyInput, opts nexus.StartOperationOptions) (TemporalOperationResult[MyOutput], error) {
//			return StartWorkflow[MyOutput](ctx, nc,
//				client.StartWorkflowOptions{ID: "wf-" + input.ID},
//				MyWorkflow, input)
//		},
//	})
//
// Synchronous operation:
//
//	op, err := NewTemporalOperation(TemporalOperationOptions[string, string]{
//		Name: "my-sync-op",
//		Start: func(ctx context.Context, nc NexusClient, input string, opts nexus.StartOperationOptions) (TemporalOperationResult[string], error) {
//			return NewSyncResult("result: " + input), nil
//		},
//	})
type TemporalOperationOptions[I, O any] struct {
	// Name of the operation. Required.
	Name string
	// Start handles the operation start. It receives the operation context, a [NexusClient] scoped
	// to this invocation, the operation input, and the Nexus start operation options.
	// Required.
	Start func(ctx context.Context, nc NexusClient, input I, options nexus.StartOperationOptions) (TemporalOperationResult[O], error)
	// CancelWorkflowRun handles cancel for workflow-run operation tokens.
	// It receives a NexusClient and the workflow ID extracted from the token.
	// If nil, defaults to cancelling the workflow via the Temporal client.
	CancelWorkflowRun func(ctx context.Context, nc NexusClient, workflowID string, options nexus.CancelOperationOptions) error
}

// NewTemporalOperation creates a generic Nexus operation backed by Temporal.
// The returned operation satisfies nexus.Operation[I, O] for service registration.
func NewTemporalOperation[I, O any](opts TemporalOperationOptions[I, O]) (nexus.Operation[I, O], error) {
	if opts.Name == "" {
		return nil, errors.New("invalid options: Name is required")
	}
	if strings.HasPrefix(opts.Name, "__temporal_") {
		return nil, errors.New("invalid options: __temporal_ is a reserved prefix")
	}
	if opts.Start == nil {
		return nil, errors.New("invalid options: Start is required")
	}
	if opts.CancelWorkflowRun == nil {
		opts.CancelWorkflowRun = defaultCancelWorkflowRun
	}
	return &temporalOperation[I, O]{options: opts}, nil
}

// MustNewTemporalOperation creates a generic Nexus operation backed by Temporal.
// Panics if invalid options are provided.
func MustNewTemporalOperation[I, O any](opts TemporalOperationOptions[I, O]) nexus.Operation[I, O] {
	op, err := NewTemporalOperation(opts)
	if err != nil {
		panic(err)
	}
	return op
}

// defaultCancelWorkflowRun is the default cancel handler for workflow-run operation tokens.
func defaultCancelWorkflowRun(ctx context.Context, nc NexusClient, workflowID string, _ nexus.CancelOperationOptions) error {
	return nc.client.CancelWorkflow(ctx, workflowID, "")
}

// temporalOperation implements nexus.Operation[I, O] for a generic Temporal-backed operation.
type temporalOperation[I, O any] struct {
	nexus.UnimplementedOperation[I, O]

	options TemporalOperationOptions[I, O]
}

func (o *temporalOperation[I, O]) Name() string {
	return o.options.Name
}

// Start begins a Nexus operation by constructing a NexusClient and delegating to the user's Start function.
func (o *temporalOperation[I, O]) Start(
	ctx context.Context,
	input I,
	options nexus.StartOperationOptions,
) (nexus.HandlerStartOperationResult[O], error) {
	// Prevent the test env client from panicking.
	ctx = context.WithValue(ctx, internal.IsWorkflowRunOpContextKey, true)

	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nil, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	started := false
	nc := NexusClient{
		client:                GetClient(ctx),
		namespace:             nctx.Namespace,
		taskQueue:             nctx.TaskQueue,
		startOperationOptions: options,
		asyncStarted:          &started,
	}

	result, err := o.options.Start(ctx, nc, input, options)
	if err != nil {
		return nil, err
	}

	return result.toHandlerResult()
}

// Cancel parses the operation token, dispatches to the appropriate per-token-type cancel handler.
func (o *temporalOperation[I, O]) Cancel(ctx context.Context, token string, options nexus.CancelOperationOptions) error {
	// Prevent the test env client from panicking.
	ctx = context.WithValue(ctx, internal.IsWorkflowRunOpContextKey, true)

	tokenType, err := loadTokenType(token)
	if err != nil {
		return &nexus.HandlerError{
			Type:    nexus.HandlerErrorTypeBadRequest,
			Message: "invalid operation token",
			Cause:   err,
		}
	}

	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	nc := NexusClient{
		client:    GetClient(ctx),
		namespace: nctx.Namespace,
		taskQueue: nctx.TaskQueue,
	}

	switch tokenType {
	case operationTokenTypeWorkflowRun:
		wfToken, err := loadWorkflowRunOperationToken(token)
		if err != nil {
			return &nexus.HandlerError{
				Type:    nexus.HandlerErrorTypeBadRequest,
				Message: "invalid operation token",
				Cause:   err,
			}
		}
		return o.options.CancelWorkflowRun(ctx, nc, wfToken.WorkflowID, options)
	default:
		return nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "unknown operation token type: %d", tokenType)
	}
}
