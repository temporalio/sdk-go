package temporalnexus

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/workflow"
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

// CancelTemporalActivityExecutionOptions are options provided to the CancelActivityExecution
// callback of a Temporal Nexus operation.
//
// NOTE: Experimental
type CancelTemporalActivityExecutionOptions struct {
	// ActivityID extracted from the operation token.
	ActivityID string
	// RunID extracted from the operation token. May be empty for tokens generated before the
	// activity was started (e.g. callback tokens).
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

// StartActivity schedules a stand-alone activity execution for a Nexus operation and returns an
// async result with an activity-execution operation token.
//
// The activity parameter must have the signature func(context.Context, I) (O, error). For
// activities that don't follow this signature, use [StartUntypedActivity].
//
// activityOpts must specify a TaskQueue (defaults to the current worker's task queue when empty)
// and at least one of StartToCloseTimeout or ScheduleToCloseTimeout. ID is auto-generated when
// omitted.
//
// These are free functions because Go does not allow generic methods on non-generic structs.
func StartActivity[I, O any, AF func(context.Context, I) (O, error)](
	ctx context.Context,
	nc NexusClient,
	activityOpts client.StartActivityOptions,
	activity AF,
	arg I,
) (TemporalOperationResult[O], error) {
	return StartUntypedActivity[O](ctx, nc, activityOpts, activity, arg)
}

// StartUntypedActivity schedules a stand-alone activity execution for a Nexus operation by
// activity function reference or string name. It always returns an async result with an
// activity-execution operation token.
//
// Automatically propagates the callback, links, and request ID from the Nexus operation options
// to the activity start request.
//
// See [StartActivity] for the type-safe variant.
func StartUntypedActivity[R any](
	ctx context.Context,
	nc NexusClient,
	activityOpts client.StartActivityOptions,
	activity any,
	args ...any,
) (TemporalOperationResult[R], error) {
	if nc.asyncStarted != nil && !nc.asyncStarted.CompareAndSwap(false, true) {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "only one async operation can be started per operation invocation")
	}
	ctx = context.WithValue(ctx, internal.IsWorkflowRunOpContextKey, true)

	result, err := startActivity[R](ctx, nc.client, nc.startOperationOptions, activityOpts, activity, args...)
	if err != nil {
		if nc.asyncStarted != nil {
			nc.asyncStarted.Store(false)
		}
		return TemporalOperationResult[R]{}, err
	}
	return result, nil
}

func startActivity[R any](
	ctx context.Context,
	c client.Client,
	nexusOptions nexus.StartOperationOptions,
	activityOpts client.StartActivityOptions,
	activity any,
	args ...any,
) (TemporalOperationResult[R], error) {
	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return TemporalOperationResult[R]{}, nexus.NewHandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	if activityOpts.TaskQueue == "" {
		activityOpts.TaskQueue = nctx.TaskQueue
	}
	if activityOpts.ScheduleToCloseTimeout == 0 && activityOpts.StartToCloseTimeout == 0 {
		return TemporalOperationResult[R]{}, &nexus.HandlerError{
			Type:    nexus.HandlerErrorTypeBadRequest,
			Message: "at least one of StartToCloseTimeout or ScheduleToCloseTimeout is required",
		}
	}

	if nexusOptions.RequestID != "" {
		internal.SetRequestIDOnStartActivityOptions(&activityOpts, nexusOptions.RequestID)
	}

	links, err := convertNexusLinks(nexusOptions.Links, GetLogger(ctx))
	if err != nil {
		return TemporalOperationResult[R]{}, &nexus.HandlerError{
			Type:    nexus.HandlerErrorTypeBadRequest,
			Message: "could not convert links for activity start",
			Cause:   err,
		}
	}

	if nexusOptions.CallbackURL != "" {
		// Callback token is generated without a run ID since the activity hasn't started yet.
		callbackToken, err := generateActivityExecutionOperationToken(nctx.Namespace, activityOpts.ID, "")
		if err != nil {
			return TemporalOperationResult[R]{}, err
		}
		if nexusOptions.CallbackHeader == nil {
			nexusOptions.CallbackHeader = make(nexus.Header)
		}
		nexusOptions.CallbackHeader.Set(nexus.HeaderOperationToken, callbackToken)
		internal.SetCallbacksOnStartActivityOptions(&activityOpts, []*commonpb.Callback{
			{
				Variant: &commonpb.Callback_Nexus_{
					Nexus: &commonpb.Callback_Nexus{
						Url:    nexusOptions.CallbackURL,
						Header: nexusOptions.CallbackHeader,
					},
				},
				Links: links,
			},
		})
	}

	// Duplicated in links to be compatible with older servers that don't read links from callbacks.
	internal.SetLinksOnStartActivityOptions(&activityOpts, links)
	internal.SetOnConflictOptionsOnStartActivityOptions(&activityOpts)
	responseInfo := internal.SetResponseInfoOnStartActivityOptions(&activityOpts)

	handle, err := c.ExecuteActivity(ctx, activityOpts, activity, args...)
	if err != nil {
		return TemporalOperationResult[R]{}, err
	}
	encodedToken, err := generateActivityExecutionOperationToken(nctx.Namespace, handle.GetID(), handle.GetRunID())
	if err != nil {
		return TemporalOperationResult[R]{}, err
	}

	activityLink := internal.GetResponseLinkFromStartActivityResponseInfo(responseInfo).GetActivity()
	if activityLink == nil {
		activityLink = &commonpb.Link_Activity{
			Namespace:  nctx.Namespace,
			ActivityId: handle.GetID(),
			RunId:      handle.GetRunID(),
		}
	}
	nexus.AddHandlerLinks(ctx, ConvertLinkActivityToNexusLink(activityLink))
	return NewAsyncResult[R](encodedToken), nil
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
	// CancelActivityExecution handles cancel for activity-execution operation tokens.
	// It receives the Temporal client, the activity-execution options extracted from the token, and
	// the Nexus cancel options. If nil, defaults to cancelling the activity via the Temporal client.
	CancelActivityExecution func(ctx context.Context, c client.Client, options CancelTemporalActivityExecutionOptions, cancelOptions nexus.CancelOperationOptions) error
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
	if opts.CancelActivityExecution == nil {
		opts.CancelActivityExecution = defaultCancelActivityExecution
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

// defaultCancelActivityExecution is the default cancel handler for activity-execution operation tokens.
func defaultCancelActivityExecution(ctx context.Context, c client.Client, options CancelTemporalActivityExecutionOptions, _ nexus.CancelOperationOptions) error {
	handle := c.GetActivityHandle(client.GetActivityHandleOptions{
		ActivityID: options.ActivityID,
		RunID:      options.RunID,
	})
	return handle.Cancel(ctx, client.CancelActivityOptions{})
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
	case operationTokenTypeActivityExecution:
		actToken, err := loadActivityExecutionOperationToken(token)
		if err != nil {
			return &nexus.HandlerError{
				Type:    nexus.HandlerErrorTypeBadRequest,
				Message: "invalid operation token",
				Cause:   err,
			}
		}
		return o.options.CancelActivityExecution(ctx, GetClient(ctx), CancelTemporalActivityExecutionOptions{
			ActivityID: actToken.ActivityID,
			RunID:      actToken.RunID,
		}, options)
	default:
		return nexus.NewHandlerErrorf(nexus.HandlerErrorTypeBadRequest, "unknown operation token type: %d", tokenType)
	}
}
