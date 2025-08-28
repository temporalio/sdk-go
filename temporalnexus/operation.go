// Package temporalnexus provides utilities for exposing Temporal constructs as Nexus Operations.
//
// Nexus RPC is a modern open-source service framework for arbitrary-length operations whose lifetime may extend beyond
// a traditional RPC. Nexus was designed with durable execution in mind, as an underpinning to connect durable
// executions within and across namespaces, clusters and regions – with a clean API contract to streamline multi-team
// collaboration. Any service can be exposed as a set of sync or async Nexus operations – the latter provides an
// operation identity and a uniform interface to get the status of an operation or its result, receive a completion
// callback, or cancel the operation.
//
// Temporal leverages the Nexus RPC protocol to facilitate calling across namespace and cluster and boundaries.
//
// See also:
//
// Nexus over HTTP Spec: https://github.com/nexus-rpc/api/blob/main/SPEC.md
//
// Nexus Go SDK: https://github.com/nexus-rpc/sdk-go
package temporalnexus

import (
	"context"
	"errors"
	"strings"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// GetMetricsHandler returns a metrics handler to be used in a Nexus operation's context.
func GetMetricsHandler(ctx context.Context) metrics.Handler {
	return internal.GetNexusOperationMetricsHandler(ctx)
}

// GetLogger returns a logger to be used in a Nexus operation's context.
func GetLogger(ctx context.Context) log.Logger {
	return internal.GetNexusOperationLogger(ctx)
}

// GetClient returns a client to be used in a Nexus operation's context, this is the same client that the worker was
// created with. Client methods will panic when called from the test environment.
func GetClient(ctx context.Context) client.Client {
	return internal.GetNexusOperationClient(ctx)
}

// WorkflowRunOperationOptions are options for [NewWorkflowRunOperationWithOptions].
type WorkflowRunOperationOptions[I, O any] struct {
	// Operation name.
	Name string
	// Workflow function to map this operation to. The operation input maps directly to workflow input.
	// The workflow name is resolved as it would when using this function in client.ExecuteOperation.
	// GetOptions must be provided when setting this option. Mutually exclusive with Handler.
	Workflow func(workflow.Context, I) (O, error)
	// Options for starting the workflow. Must be set if Workflow is set. Mutually exclusive with Handler.
	// The options returned must include a workflow ID that is deterministically generated from the input in order
	// for the operation to be idempotent as the request to start the operation may be retried.
	// TaskQueue is optional and defaults to the current worker's task queue.
	// WorkflowExecutionErrorWhenAlreadyStarted is ignored and always set to true.
	// WorkflowIDConflictPolicy is by default set to fail if a workflow is already running. That is,
	// if a caller executes another operation that starts the same workflow, it will fail. You can set
	// it to WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING to attach the caller's callback to the existing
	// running workflow. This way, all attached callers will be notified when the workflow completes.
	GetOptions func(context.Context, I, nexus.StartOperationOptions) (client.StartWorkflowOptions, error)
	// Handler for starting a workflow with a different input than the operation. Mutually exclusive with Workflow
	// and GetOptions.
	Handler func(context.Context, I, nexus.StartOperationOptions) (WorkflowHandle[O], error)
	// Start with signal
	StartSignalName string
	// Signal argument
	StartSignalArg interface{}
}

// NOTE: not implementing GetInfo and GetResult just yet, they're not part of the supported methods in Temporal.
type workflowRunOperation[I, O any] struct {
	nexus.UnimplementedOperation[I, O]

	options WorkflowRunOperationOptions[I, O]
}

// NewWorkflowRunOperation maps an operation to a workflow run.
func NewWorkflowRunOperation[I, O any](
	name string,
	workflow func(workflow.Context, I) (O, error),
	getOptions func(context.Context, I, nexus.StartOperationOptions) (client.StartWorkflowOptions, error),
) nexus.Operation[I, O] {
	if strings.HasPrefix(name, "__temporal_") {
		panic(errors.New("temporalnexus NewWorkflowRunOperation __temporal_ is an invalid name"))
	}
	return &workflowRunOperation[I, O]{
		options: WorkflowRunOperationOptions[I, O]{
			Name:       name,
			Workflow:   workflow,
			GetOptions: getOptions,
		},
	}
}

// NewWorkflowRunOperation map an operation to a workflow run with the given options.
// Returns an error if invalid options are provided.
func NewWorkflowRunOperationWithOptions[I, O any](options WorkflowRunOperationOptions[I, O]) (nexus.Operation[I, O], error) {
	if options.Name == "" {
		return nil, errors.New("invalid options: Name is required")
	}
	if strings.HasPrefix(options.Name, "__temporal_") {
		return nil, errors.New("invalid options: __temporal_ is a reserved prefix")
	}
	if options.Workflow == nil && options.GetOptions == nil && options.Handler == nil {
		return nil, errors.New("invalid options: either GetOptions and Workflow, or Handler are required")
	}
	if options.Workflow != nil && options.GetOptions == nil || options.Workflow == nil && options.GetOptions != nil {
		return nil, errors.New("invalid options: must provide both Workflow and GetOptions")
	}
	if options.Handler != nil && options.Workflow != nil || options.Handler == nil && options.Workflow == nil {
		return nil, errors.New("invalid options: Workflow is mutually exclusive with Handler")
	}
	return &workflowRunOperation[I, O]{
		options: options,
	}, nil
}

// MustNewWorkflowRunOperation map an operation to a workflow run with the given options.
// Panics if invalid options are provided.
func MustNewWorkflowRunOperationWithOptions[I, O any](options WorkflowRunOperationOptions[I, O]) nexus.Operation[I, O] {
	op, err := NewWorkflowRunOperationWithOptions(options)
	if err != nil {
		panic(err)
	}
	return op
}

func (*workflowRunOperation[I, O]) Cancel(ctx context.Context, token string, options nexus.CancelOperationOptions) error {
	// Prevent the test env client from panicking when we try to use it from a workflow run operation.
	ctx = context.WithValue(ctx, internal.IsWorkflowRunOpContextKey, true)

	var workflowID string
	workflowRunToken, err := loadWorkflowRunOperationToken(token)
	if err != nil {
		return &nexus.HandlerError{
			Type:  nexus.HandlerErrorTypeBadRequest,
			Cause: err,
		}
	} else {
		workflowID = workflowRunToken.WorkflowID
	}

	return GetClient(ctx).CancelWorkflow(ctx, workflowID, "")
}

func (o *workflowRunOperation[I, O]) Name() string {
	return o.options.Name
}

// Start begins an async Nexus operation backed by a workflow.
// The Operation ID returned in the response should not be modified because it is used for cancelation and reporting
// completion.
func (o *workflowRunOperation[I, O]) Start(
	ctx context.Context,
	input I,
	options nexus.StartOperationOptions,
) (nexus.HandlerStartOperationResult[O], error) {
	// Prevent the test env client from panicking when we try to use it from a workflow run operation.
	ctx = context.WithValue(ctx, internal.IsWorkflowRunOpContextKey, true)

	_, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nil, nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	if o.options.Handler != nil {
		handle, err := o.options.Handler(ctx, input, options)
		if err != nil {
			return nil, err
		}
		nexus.AddHandlerLinks(ctx, handle.link())
		return &nexus.HandlerStartOperationResultAsync{
			OperationToken: handle.token(),
			OperationID:    handle.token(),
		}, nil
	}

	wfOpts, err := o.options.GetOptions(ctx, input, options)
	if err != nil {
		return nil, err
	}
	handle, err := ExecuteWorkflow(ctx, options, wfOpts, o.options.StartSignalName, o.options.StartSignalArg, o.options.Workflow, input)
	if err != nil {
		return nil, err
	}

	nexus.AddHandlerLinks(ctx, handle.link())
	return &nexus.HandlerStartOperationResultAsync{
		OperationToken: handle.token(),
		OperationID:    handle.token(),
	}, nil
}

// WorkflowHandle is a readonly representation of a workflow run backing a Nexus operation.
// It's created via the [ExecuteWorkflow] and [ExecuteUntypedWorkflow] methods.
type WorkflowHandle[T any] interface {
	// ID is the workflow's ID.
	ID() string
	// ID is the workflow's run ID.
	RunID() string

	/* Methods below intentionally not exposed, interface is not meant to be implementable outside of this package */

	// Link to the WorkflowExecutionStarted event of the workflow represented by this handle.
	link() nexus.Link
	token() string // Cached operation token
}

type workflowHandle[T any] struct {
	namespace   string
	id          string
	runID       string
	wfEventLink *common.Link
	cachedToken string
}

func (h workflowHandle[T]) ID() string {
	return h.id
}

func (h workflowHandle[T]) RunID() string {
	return h.runID
}

func (h workflowHandle[T]) link() nexus.Link {
	// Create the link information about the workflow and return to the caller.
	link := h.wfEventLink.GetWorkflowEvent()
	if link == nil {
		link = &common.Link_WorkflowEvent{
			Namespace:  h.namespace,
			WorkflowId: h.ID(),
			RunId:      h.RunID(),
			Reference: &common.Link_WorkflowEvent_EventRef{
				EventRef: &common.Link_WorkflowEvent_EventReference{
					EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
				},
			},
		}
	}
	return ConvertLinkWorkflowEventToNexusLink(link)
}

func (h workflowHandle[T]) token() string {
	return h.cachedToken
}

// ExecuteWorkflow starts a workflow run for a [WorkflowRunOperationOptions] Handler, linking the execution chain to a
// Nexus operation (subsequent runs started from continue-as-new and retries).
// Automatically propagates the callback and request ID from the nexus options to the workflow.
func ExecuteWorkflow[I, O any, WF func(workflow.Context, I) (O, error)](
	ctx context.Context,
	nexusOptions nexus.StartOperationOptions,
	startWorkflowOptions client.StartWorkflowOptions,
	signalName string,
	signalArg interface{},
	workflow WF,
	arg I,
) (WorkflowHandle[O], error) {
	return ExecuteUntypedWorkflow[O](ctx, nexusOptions, startWorkflowOptions, signalName, signalArg, workflow, arg)
}

// ExecuteUntypedWorkflow starts a workflow with by function reference or string name, linking the execution chain to a
// Nexus operation.
// Useful for invoking workflows that don't follow the single argument - single return type signature.
// See [ExecuteWorkflow] for more information.
func ExecuteUntypedWorkflow[R any](
	ctx context.Context,
	nexusOptions nexus.StartOperationOptions,
	startWorkflowOptions client.StartWorkflowOptions,
	signalName string,
	signalArg interface{},
	workflow any,
	args ...any,
) (WorkflowHandle[R], error) {
	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nil, nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	workflowType, err := nctx.ResolveWorkflowName(workflow)
	if err != nil {
		panic(err)
	}

	if startWorkflowOptions.TaskQueue == "" {
		startWorkflowOptions.TaskQueue = nctx.TaskQueue
	}
	if startWorkflowOptions.ID == "" {
		return nil, internal.ErrMissingWorkflowID
	}

	if nexusOptions.RequestID != "" {
		internal.SetRequestIDOnStartWorkflowOptions(&startWorkflowOptions, nexusOptions.RequestID)
	}

	links, err := convertNexusLinks(nexusOptions.Links, GetLogger(ctx))
	if err != nil {
		return nil, &nexus.HandlerError{
			Type:  nexus.HandlerErrorTypeBadRequest,
			Cause: err,
		}
	}

	encodedToken, err := generateWorkflowRunOperationToken(nctx.Namespace, startWorkflowOptions.ID)
	if err != nil {
		return nil, err
	}
	if nexusOptions.CallbackURL != "" {
		if nexusOptions.CallbackHeader == nil {
			nexusOptions.CallbackHeader = make(nexus.Header)
		}

		//lint:ignore SA1019 this field is expected to be populated by servers older than 1.27.0.
		nexusOptions.CallbackHeader.Set(nexus.HeaderOperationID, encodedToken)
		nexusOptions.CallbackHeader.Set(nexus.HeaderOperationToken, encodedToken)
		internal.SetCallbacksOnStartWorkflowOptions(&startWorkflowOptions, []*common.Callback{
			{
				Variant: &common.Callback_Nexus_{
					Nexus: &common.Callback_Nexus{
						Url:    nexusOptions.CallbackURL,
						Header: nexusOptions.CallbackHeader,
					},
				},
				Links: links,
			},
		})
	}

	// Links are duplicated in startWorkflowOptions to backwards compatibility with older servers that
	// don't support links in callbacks.
	internal.SetLinksOnStartWorkflowOptions(&startWorkflowOptions, links)
	internal.SetOnConflictOptionsOnStartWorkflowOptions(&startWorkflowOptions)
	responseInfo := internal.SetResponseInfoOnStartWorkflowOptions(&startWorkflowOptions)

	// This makes sure that ExecuteWorkflow will respect the WorkflowIDConflictPolicy, ie., if the
	// conflict policy is to fail (default value), then ExecuteWorkflow will return an error if the
	// workflow already running. For Nexus, this ensures that operation has only started successfully
	// when the callback has been attached to the workflow (new or existing running workflow).
	startWorkflowOptions.WorkflowExecutionErrorWhenAlreadyStarted = true

	client := GetClient(ctx)
	run, err := client.ExecuteWorkflow(ctx, startWorkflowOptions, workflowType, args...)
	if err != nil {
		return nil, err
	}
	if signalName != "" {
		if err = client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), signalName, signalArg); err != nil {
			return nil, err
		}
	}

	return workflowHandle[R]{
		namespace:   nctx.Namespace,
		id:          run.GetID(),
		runID:       run.GetRunID(),
		wfEventLink: responseInfo.Link,
		cachedToken: encodedToken,
	}, nil
}

func convertNexusLinks(nexusLinks []nexus.Link, log log.Logger) ([]*common.Link, error) {
	var links []*common.Link
	for _, nexusLink := range nexusLinks {
		switch nexusLink.Type {
		case string((&common.Link_WorkflowEvent{}).ProtoReflect().Descriptor().FullName()):
			link, err := ConvertNexusLinkToLinkWorkflowEvent(nexusLink)
			if err != nil {
				return nil, err
			}
			links = append(links, &common.Link{
				Variant: &common.Link_WorkflowEvent_{
					WorkflowEvent: link,
				},
			})
		default:
			log.Warn("ignoring unsupported link data type: %q", nexusLink.Type)
		}
	}
	return links, nil
}
