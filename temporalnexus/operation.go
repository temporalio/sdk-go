// The MIT License
//
// Copyright (c) 2024 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		panic("temporalnexus GetMetricsHandler: Not a valid Nexus context")
	}
	return nctx.MetricsHandler
}

// GetLogger returns a logger to be used in a Nexus operation's context.
func GetLogger(ctx context.Context) log.Logger {
	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		panic("temporalnexus GetLogger: Not a valid Nexus context")
	}
	return nctx.Log
}

type syncOperation[I, O any] struct {
	nexus.UnimplementedOperation[I, O]

	name    string
	handler func(context.Context, client.Client, I, nexus.StartOperationOptions) (O, error)
}

// NewSyncOperation is a helper for creating a synchronous-only [nexus.Operation] from a given name and handler
// function. The handler is passed the client that the worker was created with.
// Sync operations are useful for exposing short-lived Temporal client requests, such as signals, queries, sync update,
// list workflows, etc...
//
// NOTE: Experimental
func NewSyncOperation[I any, O any](
	name string,
	handler func(context.Context, client.Client, I, nexus.StartOperationOptions) (O, error),
) nexus.Operation[I, O] {
	return &syncOperation[I, O]{
		name:    name,
		handler: handler,
	}
}

func (o *syncOperation[I, O]) Name() string {
	return o.name
}

func (o *syncOperation[I, O]) Start(ctx context.Context, input I, options nexus.StartOperationOptions) (nexus.HandlerStartOperationResult[O], error) {
	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nil, nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}
	out, err := o.handler(ctx, nctx.Client, input, options)
	if err != nil {
		return nil, err
	}
	return &nexus.HandlerStartOperationResultSync[O]{Value: out}, err
}

// WorkflowSignalInput encapsulates the values required to send a signal to a workflow.
//
// NOTE: Experimental
type WorkflowSignalInput struct {
	// WorkflowID is the ID of the workflow which will receive the signal. Required.
	WorkflowID string
	// RunID is the run ID of the workflow which will receive the signal. Optional. If empty, the signal will be
	// delivered to the running execution of the indicated workflow ID.
	RunID string
	// SignalName is the name of the signal. Required.
	SignalName string
	// Arg is the payload attached to the signal. Optional.
	Arg any
}

type WorkflowSignalOperationOptions[I, O any] struct {
	// Name of the operation.
	Name string

	// GetSignalInput is a function to map the operation input to the parameters required to send a signal to a workflow.
	// Mutually exclusive with Handler. See WorkflowSignalInput for details.
	GetSignalInput func(context.Context, I, nexus.StartOperationOptions) (WorkflowSignalInput, error)

	// Workflow and GetStartOptions should be set to indicate SignalWithStartWorkflowExecution should be used. Optional.
	// If not set, SignalWorkflow will be used.
	// Workflow function to map this operation to. The operation input maps directly to workflow input.
	// The workflow name is resolved as it would when using this function in client.ExecuteOperation.
	// GetOptions must be provided when setting this option. Mutually exclusive with Handler.
	Workflow func(workflow.Context, I) (O, error)
	// Options for starting the workflow. Must be set if Workflow is set. Mutually exclusive with Handler.
	// If the options returned include a workflow ID, it must match the workflow ID set in the signal input. That
	// workflow ID must be deterministically generated from the input in order for the operation to be idempotent as
	// the request to start the operation may be retried. TaskQueue is optional and defaults to the current worker's
	// task queue.
	GetStartOptions func(context.Context, I, nexus.StartOperationOptions) (client.StartWorkflowOptions, error)

	// Handler for starting a workflow with a different input than the operation. Mutually exclusive with
	// GetSignalInput, Workflow and GetStartOptions.
	Handler func(context.Context, I, nexus.StartOperationOptions) (nexus.HandlerStartOperationResult[O], error)
}

type workflowSignalOperation[I, O any] struct {
	nexus.UnimplementedOperation[I, O]

	options WorkflowSignalOperationOptions[I, O]
}

// NewWorkflowSignalOperation is a helper for creating a synchronous nexus.Operation to deliver a signal, linking the
// signal to a Nexus operation. Request ID from the Nexus options is propagated to the workflow to ensure idempotency.
// The operation is complete as soon as the signal is delivered and returns no value.
//
// NOTE: Experimental
func NewWorkflowSignalOperation[I any](
	name string,
	getSignalInput func(context.Context, I, nexus.StartOperationOptions) (WorkflowSignalInput, error),
) nexus.Operation[I, nexus.NoValue] {
	return &workflowSignalOperation[I, nexus.NoValue]{
		options: WorkflowSignalOperationOptions[I, nexus.NoValue]{
			Name:           name,
			GetSignalInput: getSignalInput,
		},
	}
}

// NewWorkflowSignalWithStartOperation is a helper for creating a synchronous nexus.Operation to deliver a signal to a
// workflow. If the indicated workflow is not running, a new run will be started. The workflow execution chain and
// signal will be linked to a Nexus operation. Request ID from the Nexus options is propagated to the workflow to
// ensure idempotency. The operation is complete as soon as the signal is delivered and returns no value.
//
// NOTE: Experimental
func NewWorkflowSignalWithStartOperation[I any](
	name string,
	getSignalInput func(context.Context, I, nexus.StartOperationOptions) (WorkflowSignalInput, error),
	workflow func(workflow.Context, I) (nexus.NoValue, error),
	getStartOptions func(context.Context, I, nexus.StartOperationOptions) (client.StartWorkflowOptions, error),
) nexus.Operation[I, nexus.NoValue] {
	return &workflowSignalOperation[I, nexus.NoValue]{
		options: WorkflowSignalOperationOptions[I, nexus.NoValue]{
			Name:            name,
			GetSignalInput:  getSignalInput,
			Workflow:        workflow,
			GetStartOptions: getStartOptions,
		},
	}
}

// NewWorkflowSignalOperationWithOptions maps an operation to a SignalWorkflow or SignalWithStartWorkflow request with
// the given options. Returns an error if invalid options are provided.
//
// NOTE: Experimental
func NewWorkflowSignalOperationWithOptions[I, O any](options WorkflowSignalOperationOptions[I, O]) (nexus.Operation[I, O], error) {
	if options.Name == "" {
		return nil, errors.New("invalid options: Name is required")
	}
	if options.GetSignalInput == nil && options.Handler == nil {
		return nil, errors.New("invalid options: either GetSignalInput or Handler are required")
	}
	if options.Workflow != nil && options.GetStartOptions == nil || options.Workflow == nil && options.GetStartOptions != nil {
		return nil, errors.New("invalid options: must provide both Workflow and GetOptions")
	}
	if options.Handler != nil && options.GetSignalInput != nil {
		return nil, errors.New("invalid options: GetSignalInput is mutually exclusive with Handler")
	}
	return &workflowSignalOperation[I, O]{
		options: options,
	}, nil
}

func (o *workflowSignalOperation[I, O]) Name() string {
	return o.options.Name
}

func (o *workflowSignalOperation[I, O]) Start(
	ctx context.Context,
	input I,
	options nexus.StartOperationOptions,
) (nexus.HandlerStartOperationResult[O], error) {
	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nil, nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	if o.options.Handler != nil {
		return o.options.Handler(ctx, input, options)
	}

	signalInput, err := o.options.GetSignalInput(ctx, input, options)
	if err != nil {
		return nil, err
	}

	if o.options.Workflow != nil {
		// Caller has indicated SignalWithStartWorkflow should be used.
		startOptions, err := o.options.GetStartOptions(ctx, input, options)
		if err != nil {
			return nil, err
		}

		if startOptions.ID != "" && startOptions.ID != signalInput.WorkflowID {
			return nil, nexus.HandlerErrorf(nexus.HandlerErrorTypeBadRequest, "signal target workflow ID: %s does not match start workflow options ID: %s", signalInput.WorkflowID, startOptions.ID)
		}

		err = SignalWithStartUntypedWorkflow(ctx, options, startOptions, signalInput.SignalName, signalInput.Arg, o.options.Workflow, input)
		if err != nil {
			return nil, err
		}
	} else {
		// Caller has indicated SignalWorkflow should be used.
		// RequestID and Links must be propagated through context because the SignalWorkflow interface does not support
		// passing them directly. For SignalWithStart they are passed in the StartWorkflowOptions.
		if options.RequestID != "" {
			ctx = context.WithValue(ctx, internal.NexusOperationRequestIDKey, options.RequestID)
		}

		links, err := convertNexusLinks(options.Links, GetLogger(ctx))
		if err != nil {
			return nil, err
		}
		ctx = context.WithValue(ctx, internal.NexusOperationLinksKey, links)

		err = nctx.Client.SignalWorkflow(ctx, signalInput.WorkflowID, signalInput.RunID, signalInput.SignalName, signalInput.Arg)
		if err != nil {
			return nil, err
		}
	}

	// TODO(pj): return links
	return &nexus.HandlerStartOperationResultSync[nexus.NoValue]{}, nil
}

// WorkflowRunOperationOptions are options for [NewWorkflowRunOperationWithOptions].
//
// NOTE: Experimental
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
	GetOptions func(context.Context, I, nexus.StartOperationOptions) (client.StartWorkflowOptions, error)
	// Handler for starting a workflow with a different input than the operation. Mutually exclusive with Workflow
	// and GetOptions.
	Handler func(context.Context, I, nexus.StartOperationOptions) (WorkflowHandle[O], error)
}

// NOTE: not implementing GetInfo and GetResult just yet, they're not part of the supported methods in Temporal.
type workflowRunOperation[I, O any] struct {
	nexus.UnimplementedOperation[I, O]

	options WorkflowRunOperationOptions[I, O]
}

// NewWorkflowRunOperation maps an operation to a workflow run.
//
// NOTE: Experimental
func NewWorkflowRunOperation[I, O any](
	name string,
	workflow func(workflow.Context, I) (O, error),
	getOptions func(context.Context, I, nexus.StartOperationOptions) (client.StartWorkflowOptions, error),
) nexus.Operation[I, O] {
	return &workflowRunOperation[I, O]{
		options: WorkflowRunOperationOptions[I, O]{
			Name:       name,
			Workflow:   workflow,
			GetOptions: getOptions,
		},
	}
}

// NewWorkflowRunOperationWithOptions maps an operation to a workflow run with the given options.
// Returns an error if invalid options are provided.
//
// NOTE: Experimental
func NewWorkflowRunOperationWithOptions[I, O any](options WorkflowRunOperationOptions[I, O]) (nexus.Operation[I, O], error) {
	if options.Name == "" {
		return nil, errors.New("invalid options: Name is required")
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

// MustNewWorkflowRunOperationWithOptions maps an operation to a workflow run with the given options.
// Panics if invalid options are provided.
//
// NOTE: Experimental
func MustNewWorkflowRunOperationWithOptions[I, O any](options WorkflowRunOperationOptions[I, O]) nexus.Operation[I, O] {
	op, err := NewWorkflowRunOperationWithOptions[I, O](options)
	if err != nil {
		panic(err)
	}
	return op
}

func (*workflowRunOperation[I, O]) Cancel(ctx context.Context, id string, options nexus.CancelOperationOptions) error {
	// Prevent the test env client from panicking when we try to use it from a workflow run operation.
	ctx = context.WithValue(ctx, internal.IsWorkflowRunOpContextKey, true)

	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}
	return nctx.Client.CancelWorkflow(ctx, id, "")
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
		return &nexus.HandlerStartOperationResultAsync{
			OperationID: handle.ID(),
			Links:       []nexus.Link{handle.link()},
		}, nil
	}

	wfOpts, err := o.options.GetOptions(ctx, input, options)
	if err != nil {
		return nil, err
	}

	handle, err := ExecuteWorkflow(ctx, options, wfOpts, o.options.Workflow, input)
	if err != nil {
		return nil, err
	}

	return &nexus.HandlerStartOperationResultAsync{
		OperationID: handle.ID(),
		Links:       []nexus.Link{handle.link()},
	}, nil
}

// WorkflowHandle is a readonly representation of a workflow run backing a Nexus operation.
// It's created via the [ExecuteWorkflow] and [ExecuteUntypedWorkflow] methods.
//
// NOTE: Experimental
type WorkflowHandle[T any] interface {
	// ID is the workflow's ID.
	ID() string
	// ID is the workflow's run ID.
	RunID() string

	/* Methods below intentionally not exposed, interface is not meant to be implementable outside of this package */

	// Link to the WorkflowExecutionStarted event of the workflow represented by this handle.
	link() nexus.Link
}

type workflowHandle[T any] struct {
	namespace string
	id        string
	runID     string
}

func (h workflowHandle[T]) ID() string {
	return h.id
}

func (h workflowHandle[T]) RunID() string {
	return h.runID
}

func (h workflowHandle[T]) link() nexus.Link {
	// Create the link information about the new workflow and return to the caller.
	link := &common.Link_WorkflowEvent{
		Namespace:  h.namespace,
		WorkflowId: h.ID(),
		RunId:      h.RunID(),
		Reference: &common.Link_WorkflowEvent_EventRef{
			EventRef: &common.Link_WorkflowEvent_EventReference{
				EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			},
		},
	}
	return ConvertLinkWorkflowEventToNexusLink(link)

}

// ExecuteWorkflow starts a workflow run for a [WorkflowRunOperationOptions] Handler, linking the execution chain to a
// Nexus operation (subsequent runs started from continue-as-new and retries).
// Automatically propagates the callback and request ID from the nexus options to the workflow.
//
// NOTE: Experimental
func ExecuteWorkflow[I, O any, WF func(workflow.Context, I) (O, error)](
	ctx context.Context,
	nexusOptions nexus.StartOperationOptions,
	startWorkflowOptions client.StartWorkflowOptions,
	workflow WF,
	arg I,
) (WorkflowHandle[O], error) {
	return ExecuteUntypedWorkflow[O](ctx, nexusOptions, startWorkflowOptions, workflow, arg)
}

// ExecuteUntypedWorkflow starts a workflow with by function reference or string name, linking the execution chain to a
// Nexus operation.
// Useful for invoking workflows that don't follow the single argument - single return type signature.
// See [ExecuteWorkflow] for more information.
//
// NOTE: Experimental
func ExecuteUntypedWorkflow[R any](
	ctx context.Context,
	nexusOptions nexus.StartOperationOptions,
	startWorkflowOptions client.StartWorkflowOptions,
	workflow any,
	args ...any,
) (WorkflowHandle[R], error) {
	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nil, nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	workflowType, err := prepareStartWorkflowOptions(nctx, nexusOptions, &startWorkflowOptions, workflow)
	if err != nil {
		return nil, err
	}

	run, err := nctx.Client.ExecuteWorkflow(ctx, startWorkflowOptions, workflowType, args...)
	if err != nil {
		return nil, err
	}
	return workflowHandle[R]{
		namespace: nctx.Namespace,
		id:        run.GetID(),
		runID:     run.GetRunID(),
	}, nil
}

func SignalWithStartUntypedWorkflow(
	ctx context.Context,
	nexusOptions nexus.StartOperationOptions,
	startWorkflowOptions client.StartWorkflowOptions,
	signalName string,
	signalArg any,
	workflow any,
	args ...any,
) error {
	nctx, ok := internal.NexusOperationContextFromGoContext(ctx)
	if !ok {
		return nexus.HandlerErrorf(nexus.HandlerErrorTypeInternal, "internal error")
	}

	workflowType, err := prepareStartWorkflowOptions(nctx, nexusOptions, &startWorkflowOptions, workflow)
	if err != nil {
		return err
	}

	_, err = nctx.Client.SignalWithStartWorkflow(ctx, startWorkflowOptions.ID, signalName, signalArg, startWorkflowOptions, workflowType, args...)
	return err
}

func prepareStartWorkflowOptions(
	nctx *internal.NexusOperationContext,
	nexusOptions nexus.StartOperationOptions,
	startWorkflowOptions *client.StartWorkflowOptions,
	workflow any,
) (string, error) {
	workflowType, err := nctx.ResolveWorkflowName(workflow)
	if err != nil {
		panic(err)
	}

	if startWorkflowOptions.TaskQueue == "" {
		startWorkflowOptions.TaskQueue = nctx.TaskQueue
	}
	if startWorkflowOptions.ID == "" {
		return "", internal.ErrMissingWorkflowID
	}

	if nexusOptions.RequestID != "" {
		internal.SetRequestIDOnStartWorkflowOptions(startWorkflowOptions, nexusOptions.RequestID)
	}

	if nexusOptions.CallbackURL != "" {
		if nexusOptions.CallbackHeader == nil {
			nexusOptions.CallbackHeader = make(nexus.Header)
		}
		if idHeader := nexusOptions.CallbackHeader.Get(nexus.HeaderOperationID); idHeader == "" {
			nexusOptions.CallbackHeader.Set(nexus.HeaderOperationID, startWorkflowOptions.ID)
		}
		internal.SetCallbacksOnStartWorkflowOptions(startWorkflowOptions, []*common.Callback{
			{
				Variant: &common.Callback_Nexus_{
					Nexus: &common.Callback_Nexus{
						Url:    nexusOptions.CallbackURL,
						Header: nexusOptions.CallbackHeader,
					},
				},
			},
		})
	}

	links, err := convertNexusLinks(nexusOptions.Links, nctx.Log)
	if err != nil {
		return "", err
	}
	internal.SetLinksOnStartWorkflowOptions(startWorkflowOptions, links)

	return workflowType, nil
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
