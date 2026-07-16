package temporalnexus

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/workflow"
)

var (
	ErrCannotCancelWorkflowUpdate = &nexus.HandlerError{
		Type:  nexus.HandlerErrorTypeNotImplemented,
		Cause: errors.New("cannot cancel an UpdateWorkflow operation"),
	}
)

// StartTemporalOperationOptions are options provided to the Start callback of a Temporal Nexus operation.
// Mirrors [nexus.StartOperationOptions].
//
// NOTE: Experimental
type StartTemporalOperationOptions struct {
	// Header contains the request header fields received by the server.
	//
	// Header keys with the "content-" prefix are reserved for [nexus.Serializer] headers and are not
	// available here.
	Header nexus.Header
	// CallbackURL is used to deliver completion of async operations.
	CallbackURL string
	// CallbackHeader contains header fields set by the client that are required to be attached to the
	// callback request when an asynchronous operation completes.
	CallbackHeader nexus.Header
	// RequestID may be used by the server handler to dedupe a start request.
	RequestID string
	// Links contain arbitrary caller information. Handlers may use these links as metadata on
	// resources associated with an operation.
	Links []nexus.Link
}

// CancelTemporalWorkflowRunOptions are options provided to the CancelWorkflowRun callback of a
// Temporal Nexus operation.
//
// NOTE: Experimental
type CancelTemporalWorkflowRunOptions struct {
	// WorkflowID extracted from the operation token.
	WorkflowID string
}

// CancelTemporalUpdateWorkflowOptions are options provided to CancelWorkflowUpdate callback
// of a Temporal Nexus Operation
//
// NOTE: Experimental
type CancelTemporalUpdateWorkflowOptions struct {
	// WorkflowID extracted from the operation token.
	WorkflowID string
	// UpdateID to be cancelled.
	UpdateID string
	// RunID of the workflow
	RunID string
}

// TemporalOperationResult encapsulates either a synchronous result or an asynchronous operation token.
// Exactly one of sync or token must be set.
//
// NOTE: Experimental
type TemporalOperationResult[R any] struct {
	sync  *R
	token string
}

// NewSyncResult creates a TemporalOperationResult with a synchronous value.
//
// NOTE: Experimental
func NewSyncResult[R any](value R) TemporalOperationResult[R] {
	return TemporalOperationResult[R]{sync: &value}
}

// NewAsyncResult creates a TemporalOperationResult with an asynchronous operation token.
//
// NOTE: Experimental
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
//
// NOTE: Experimental
type NexusClient struct {
	client                client.Client
	startOperationOptions nexus.StartOperationOptions
	asyncStarted          *atomic.Bool
}

// GetWorkflowClient returns the underlying Temporal client for advanced or fallback use cases.
//
// NOTE: Experimental
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
//
// NOTE: Experimental
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
//
// NOTE: Experimental
func StartUntypedWorkflow[R any](
	ctx context.Context,
	nc NexusClient,
	workflowOpts client.StartWorkflowOptions,
	workflow any,
	args ...any,
) (TemporalOperationResult[R], error) {
	if nc.asyncStarted != nil && !nc.asyncStarted.CompareAndSwap(false, true) {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "only one async operation can be started per operation invocation")
	}

	// Prevent the test env client from panicking when we try to use it from an operation.
	ctx = context.WithValue(ctx, internal.IsWorkflowRunOpContextKey, true)

	handle, err := ExecuteUntypedWorkflow[R](ctx, nc.startOperationOptions, workflowOpts, workflow, args...)
	if err != nil {
		if nc.asyncStarted != nil {
			nc.asyncStarted.Store(false)
		}
		return TemporalOperationResult[R]{}, err
	}
	nexus.AddHandlerLinks(ctx, handle.link())
	return NewAsyncResult[R](handle.token()), nil
}

// StartUpdateWorkflow starts a type-safe workflow update run for a Nexus operation
// linking the execution chain to the Nexus operation (callbacks, links, request ID).
// It returns either an async result with a workflow update operation token for pending
// operations or a sync response if the update is already completed.
//
// These are free functions because Go does not allow generic methods on non-generic structs.
//
// NOTE: Experimental
func StartUpdateWorkflow[R any](
	ctx context.Context,
	nc NexusClient,
	updateWorkflowOptions client.UpdateWorkflowOptions,
) (TemporalOperationResult[R], error) {
	if updateWorkflowOptions.UpdateID == "" {
		// If an Update ID is not provided, use the RequestID. This is done to protect
		// against cases where an unset UpdateID request with the same RequestID is
		// retried due to network glitch and gets new IDs in createUpdateWorkflowInput
		updateWorkflowOptions.UpdateID = nc.startOperationOptions.RequestID
	}

	if err := validateUpdateWorkflowNexusOperation(updateWorkflowOptions); err != nil {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(
			nexus.HandlerErrorTypeInternal, "invalid update request: %w", err)
	}

	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(
			nexus.HandlerErrorTypeInternal, "internal error")
	}

	opFailed := true
	if nc.startOperationOptions.CallbackURL == "" {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest,
			"callback URL required for async UpdateWorkflow operation invocations")
	}
	if nc.asyncStarted == nil {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal,
			"unexpected error initializing async UpdateWorkflow operation")
	}
	if !nc.asyncStarted.CompareAndSwap(false, true) {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest,
			"only one async operation can be started per operation invocation")
	}
	defer func() {
		if opFailed {
			nc.asyncStarted.Store(false)
		}
	}()
	token, err := generateUpdateOperationToken(nctx.Namespace,
		updateWorkflowOptions.WorkflowID,
		updateWorkflowOptions.RunID,
		updateWorkflowOptions.UpdateID)
	if err != nil {
		return TemporalOperationResult[R]{}, err
	}

	links, err := convertNexusLinks(nc.startOperationOptions.Links, GetLogger(ctx))
	if err != nil {
		return TemporalOperationResult[R]{}, &nexus.HandlerError{
			Type:    nexus.HandlerErrorTypeBadRequest,
			Message: "could not convert links for update workflow",
			Cause:   err,
		}
	}
	internal.SetLinksOnNexusOperation(&updateWorkflowOptions, links)
	internal.SetRequestIDOnNexusOperation(&updateWorkflowOptions, nc.startOperationOptions.RequestID)
	header := nc.startOperationOptions.CallbackHeader
	if header == nil {
		header = make(nexus.Header)
	}
	header.Set(nexus.HeaderOperationToken, token)
	internal.SetCallbacksOnNexusOperation(&updateWorkflowOptions, []*common.Callback{
		{
			Variant: &common.Callback_Nexus_{
				Nexus: &common.Callback_Nexus{
					Url:    nc.startOperationOptions.CallbackURL,
					Header: header,
				},
			},
			Links: links,
		},
	})

	responseInfo := internal.SetResponseInfoOnUpdateWorkflowOptions(&updateWorkflowOptions)

	handle, err := GetClient(ctx).UpdateWorkflow(ctx, updateWorkflowOptions)
	if err != nil {
		return TemporalOperationResult[R]{}, err
	}

	if responseInfo.Link == nil {
		return TemporalOperationResult[R]{}, &nexus.HandlerError{
			Type:  nexus.HandlerErrorTypeInternal,
			Cause: errors.New("unexpected error retrieving links from UpdateWorkflow response"),
		}
	}

	nexus.AddHandlerLinks(ctx, ConvertCommonLinkToNexusLink(responseInfo.Link))

	if internal.IsUpdateWorkflowCompleted(handle) {
		// If the update workflow handle is completed already, return a sync response.
		var result R
		if err := handle.Get(ctx, &result); err != nil {
			// Failure case: If workflow handle is completed and it has an error => its
			// an unretriable error like validation failing on the update handler.
			return TemporalOperationResult[R]{}, &nexus.OperationError{
				State:   nexus.OperationStateFailed,
				Message: err.Error(),
				Cause:   err,
			}
		}
		// Success case: Required for correctness on retried UpdateWorkflow rpc with same updateID.
		return NewSyncResult(result), nil
	}
	opFailed = false
	// regenerate token so it has the actual run ID that update is running on
	// previous generation is only to handle completion before handle is returned
	token, err = generateUpdateOperationToken(nctx.Namespace,
		updateWorkflowOptions.WorkflowID,
		handle.RunID(),
		updateWorkflowOptions.UpdateID)
	if err != nil {
		// should not happen, this is all in SDK
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal,
			"unexpected error reconstructing token %w",
			err,
		)
	}
	return NewAsyncResult[R](token), nil
}

// Validations to be performed specifically for UpdateWorkflow as a Nexus Operation.
// NOTE: UpdateID will never be empty now that RequestID for nexus op is generated if empty
func validateUpdateWorkflowNexusOperation(u client.UpdateWorkflowOptions) error {
	switch {
	case u.WorkflowID == "":
		return errors.New("workflow ID cannot be empty")
	case u.UpdateName == "":
		return errors.New("update name cannot be empty")
	case u.WaitForStage != client.WorkflowUpdateStageAccepted:
		return errors.New("nexus op workflow updates only support WorkflowUpdateStageAccepted for async updates")
	default:
		return nil
	}
}

// TemporalOperationOptions configures a generic Temporal Nexus operation.
//
// Asynchronous workflow-backed operation:
//
//	op, err := NewTemporalOperation(TemporalOperationOptions[MyInput, MyOutput]{
//		Name: "my-async-op",
//		Start: func(ctx context.Context, nc NexusClient, input MyInput, opts StartTemporalOperationOptions) (TemporalOperationResult[MyOutput], error) {
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
//		Start: func(ctx context.Context, nc NexusClient, input string, opts StartTemporalOperationOptions) (TemporalOperationResult[string], error) {
//			return NewSyncResult("result: " + input), nil
//		},
//	})
//
// NOTE: Experimental
type TemporalOperationOptions[I, O any] struct {
	// Name of the operation. Required.
	Name string
	// Start handles the operation start. It receives the operation context, a [NexusClient] scoped
	// to this invocation, the operation input, and the start operation options.
	// Required.
	Start func(ctx context.Context, nc NexusClient, input I, options StartTemporalOperationOptions) (TemporalOperationResult[O], error)
	// CancelWorkflowRun handles cancel for workflow-run operation tokens.
	// It receives the Temporal client, the workflow-run options extracted from the token, and the
	// Nexus cancel options. If nil, defaults to cancelling the workflow via the Temporal client.
	CancelWorkflowRun func(ctx context.Context, c client.Client, options CancelTemporalWorkflowRunOptions, cancelOptions nexus.CancelOperationOptions) error
	// CancelWorkflowUpdate handles cancel for UpdateWorkflow operation tokens.
	// It receives the Temporal client, the update-workflow options extracted from the token, and the
	// Nexus cancel options. If nil, will error out with [ErrCannotCancelWorkflowUpdate] as there is no default behavior for cancelling an UpdateWorkflow.
	CancelWorkflowUpdate func(ctx context.Context, c client.Client, options CancelTemporalUpdateWorkflowOptions, cancelOptions nexus.CancelOperationOptions) error
}

// NewTemporalOperation creates a generic Nexus operation backed by Temporal.
// The returned operation satisfies nexus.Operation[I, O] for service registration.
//
// NOTE: Experimental
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
	if opts.CancelWorkflowUpdate == nil {
		opts.CancelWorkflowUpdate = defaultCancelWorkflowUpdate
	}
	return &temporalOperation[I, O]{options: opts}, nil
}

// MustNewTemporalOperation creates a generic Nexus operation backed by Temporal.
// Panics if invalid options are provided.
//
// NOTE: Experimental
func MustNewTemporalOperation[I, O any](opts TemporalOperationOptions[I, O]) nexus.Operation[I, O] {
	op, err := NewTemporalOperation(opts)
	if err != nil {
		panic(err)
	}
	return op
}

// defaultCancelWorkflowRun is the default cancel handler for workflow-run operation tokens.
func defaultCancelWorkflowRun(ctx context.Context, c client.Client, options CancelTemporalWorkflowRunOptions, _ nexus.CancelOperationOptions) error {
	return c.CancelWorkflow(ctx, options.WorkflowID, "")
}

// default cancel function for UpdateWorkflow returns an error
func defaultCancelWorkflowUpdate(ctx context.Context, c client.Client, options CancelTemporalUpdateWorkflowOptions, _ nexus.CancelOperationOptions) error {
	return ErrCannotCancelWorkflowUpdate
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

	if _, ok := internal.NexusOperationContextFromGoContext(ctx); !ok {
		return nil, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	if options.RequestID == "" {
		options.RequestID = uuid.NewString()
	}

	nc := NexusClient{
		client:                GetClient(ctx),
		startOperationOptions: options,
		asyncStarted:          &atomic.Bool{},
	}

	result, err := o.options.Start(ctx, nc, input, StartTemporalOperationOptions{
		Header:         options.Header,
		CallbackURL:    options.CallbackURL,
		CallbackHeader: options.CallbackHeader,
		RequestID:      options.RequestID,
		Links:          options.Links,
	})
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
		return o.options.CancelWorkflowRun(ctx, GetClient(ctx), CancelTemporalWorkflowRunOptions{
			WorkflowID: wfToken.WorkflowID,
		}, options)
	case operationTokenTypeUpdateWorkflow:
		uwfToken, err := loadUpdateWorkflowOperationToken(token)
		if err != nil {
			return &nexus.HandlerError{
				Type:    nexus.HandlerErrorTypeBadRequest,
				Message: "invalid operation token",
				Cause:   err,
			}
		}
		return o.options.CancelWorkflowUpdate(ctx, GetClient(ctx), CancelTemporalUpdateWorkflowOptions{
			WorkflowID: uwfToken.WorkflowID,
			RunID:      uwfToken.RunID,
			UpdateID:   uwfToken.UpdateID,
		}, options)
	default:
		return nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "unknown operation token type: %d", tokenType)
	}
}
