package tracing

import (
	"context"
	"errors"
	"fmt"

	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

const (
	workflowIDTagKey      = "temporalWorkflowID"
	runIDTagKey           = "temporalRunID"
	activityIDTagKey      = "temporalActivityID"
	updateIDTagKey        = "temporalUpdateID"
	terminateReasonTagKey = "temporalTerminateReason"
	nexusServiceTagKey    = "temporalNexusService"
	nexusOperationTagKey  = "temporalNexusOperation"
	nexusEndpointTagKey   = "temporalNexusEndpoint"
)

// tracerCommon contains context-independent tracing operations.
type tracerCommon interface {
	// Options returns the options for the tracer.
	Options() TracerOptions
	// UnmarshalSpan unmarshals the given map into a span reference.
	UnmarshalSpan(map[string]string) (TracerSpanRef, error)
	// MarshalSpan marshals the given span into a map. If the map is empty with no
	// error, the span is simply not set.
	MarshalSpan(TracerSpanRef) (map[string]string, error)
	// GetLogger returns a log.Logger which may include additional fields in its
	// output in order to support correlation of tracing and log data.
	GetLogger(log.Logger, TracerSpanRef) log.Logger
	SpanName(options *TracerStartSpanOptions) string
	mustEmbedBaseTracer()
}

// Tracer traces client, activity, and Nexus operations.
// Most callers should use a contrib tracing integration.
//
// All implementations must embed BaseTracer to safely handle future changes.
// Each Tracer is shared by every interceptor that uses it, so implementations
// must be safe for concurrent use.
type Tracer interface {
	tracerCommon
	// SpanFromContext returns the span from the general Go context or nil if not
	// present.
	SpanFromContext(context.Context) TracerSpanRef
	// ContextWithSpan creates a general Go context with the given span set.
	ContextWithSpan(context.Context, TracerSpanRef) context.Context
	// CreateSpan starts and returns a span with the given options.
	CreateSpan(context.Context, *TracerStartSpanOptions) TracerSpan
}

// WorkflowTracer traces workflow operations.
// Most callers should use a contrib tracing integration.
//
// All implementations must embed BaseTracer to safely handle future changes.
// Each WorkflowTracer is shared by every interceptor that uses it, so
// implementations must be safe for concurrent use.
type WorkflowTracer interface {
	tracerCommon
	// SpanFromContext returns the span from the general Go context or nil if not
	// present.
	SpanFromContext(workflow.Context) TracerSpanRef
	// ContextWithSpan creates a workflow context with the given span set.
	ContextWithSpan(workflow.Context, TracerSpanRef) workflow.Context

	// CreateSpan starts and returns a span with the given options.
	CreateSpan(workflow.Context, *TracerStartSpanOptions) TracerSpan
}

// BaseTracer is a default implementation of Tracer meant for embedding.
type BaseTracer struct{}

func (BaseTracer) GetLogger(logger log.Logger, ref TracerSpanRef) log.Logger {
	return logger
}
func (BaseTracer) SpanName(options *TracerStartSpanOptions) string {
	if options.Operation == "" {
		return options.Name
	}
	if options.Name == "" {
		return options.Operation
	}
	return fmt.Sprintf("%s:%s", options.Operation, options.Name)
}

//lint:ignore U1000 Ignore unused method; it is only required to implement the Tracer interface but will never be called.
func (BaseTracer) mustEmbedBaseTracer() {}

// TracerOptions are options returned from Tracer.Options.
type TracerOptions struct {
	// HeaderKey is the key name on the Temporal header to serialize the span to.
	// This should never be empty.
	HeaderKey string

	// AddTemporalSpans is whether to create Temporal-specific spans for
	// operations such as StartWorkflow, RunWorkflow, and RunActivity. When
	// false, trace context is still propagated through Temporal headers, so
	// spans created by application code remain connected.
	AddTemporalSpans bool

	// AllowInvalidParentSpans will swallow errors interpreting parent
	// spans from headers. Useful when migrating from one tracing library
	// to another, while workflows/activities may be in progress.
	AllowInvalidParentSpans bool
}

// SpanDirection identifies inbound or outbound spans.
type SpanDirection int

const (
	SpanDirectionUnspecified SpanDirection = iota
	SpanDirectionInbound
	SpanDirectionOutbound
)

// TracerStartSpanOptions are options for Tracer/WorkflowTracer.CreateSpan.
type TracerStartSpanOptions struct {
	// Parent is the optional parent reference of the span.
	Parent TracerSpanRef
	// Operation is the general operation name without the specific name.
	Operation string
	// Name is the specific activity, workflow, etc for the operation.
	Name string

	// DependedOn is true if the parent depends on this span or false if it just
	// is related to the parent. In OpenTracing terms, this is true for "ChildOf"
	// reference types and false for "FollowsFrom" reference types.
	DependedOn bool

	// Direction is inbound or outbound.
	Direction SpanDirection

	// Tags are a set of span tags.
	Tags map[string]string
}

// TracerSpanRef represents a span reference such as a parent.
type TracerSpanRef interface {
}

// TracerSpan represents a span.
type TracerSpan interface {
	TracerSpanRef
	// Finish is called when the span is complete.
	Finish(*TracerFinishSpanOptions)
}

// TracerFinishSpanOptions are options for TracerSpan.Finish.
type TracerFinishSpanOptions struct {
	// Error is present if there was an error in the code traced by this specific
	// span.
	Error error
}

type tracingInterceptor struct {
	tracer         Tracer
	workflowTracer WorkflowTracer
}

// NewTracingInterceptor creates client and worker tracing interceptors.
// They are returned separately so the client interceptor is not inherited onto
// workers when set on client options.
func NewTracingInterceptor(
	tracer Tracer,
	workflowTracer WorkflowTracer,
) (interceptor.ClientInterceptor, interceptor.WorkerInterceptor) {
	root := &tracingInterceptor{tracer: tracer, workflowTracer: workflowTracer}
	clientInterceptor := &tracingClientInterceptor{root: root}
	workerInterceptor := &tracingWorkerInterceptor{root: root}

	return clientInterceptor, workerInterceptor
}

type tracingClientInterceptor struct {
	interceptor.ClientInterceptorBase
	root *tracingInterceptor
}

func (t *tracingClientInterceptor) InterceptClient(
	next interceptor.ClientOutboundInterceptor,
) interceptor.ClientOutboundInterceptor {
	i := &tracingClientOutboundInterceptor{root: t.root}
	i.Next = next
	return i
}

type tracingWorkerInterceptor struct {
	interceptor.WorkerInterceptorBase
	root *tracingInterceptor
}

func (t *tracingWorkerInterceptor) InterceptActivity(
	ctx context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	i := &tracingActivityInboundInterceptor{root: t.root}
	i.Next = next
	return i
}

func (t *tracingWorkerInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	i := &tracingWorkflowInboundInterceptor{root: t.root}
	i.Next = next
	return i
}

func (t *tracingWorkerInterceptor) InterceptNexusOperation(
	ctx context.Context,
	next interceptor.NexusOperationInboundInterceptor,
) interceptor.NexusOperationInboundInterceptor {
	i := &tracingNexusOperationInboundInterceptor{root: t.root}
	i.Next = next
	return i
}

type tracingClientOutboundInterceptor struct {
	interceptor.ClientOutboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingClientOutboundInterceptor) CreateSchedule(ctx context.Context, in *interceptor.ScheduleClientCreateInput) (run client.ScheduleHandle, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "CreateSchedule",
		Name:      in.Options.ID,
	}, t.root.headerWriter(t.root.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.CreateSchedule(ctx, in)
}

func (t *tracingClientOutboundInterceptor) ExecuteWorkflow(
	ctx context.Context,
	in *interceptor.ClientExecuteWorkflowInput,
) (run client.WorkflowRun, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "StartWorkflow",
		Name:      in.WorkflowType,
		Tags:      map[string]string{workflowIDTagKey: in.Options.ID},
	}, t.root.headerWriter(t.root.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) SignalWorkflow(ctx context.Context, in *interceptor.ClientSignalWorkflowInput) (err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "SignalWorkflow",
		Name:      in.SignalName,
		Tags:      map[string]string{workflowIDTagKey: in.WorkflowID},
	}, t.root.headerWriter(t.root.tracer, ctx))
	if err != nil {
		return err
	}
	defer endSpan(&err)

	return t.Next.SignalWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) SignalWithStartWorkflow(
	ctx context.Context,
	in *interceptor.ClientSignalWithStartWorkflowInput,
) (run client.WorkflowRun, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "SignalWithStartWorkflow",
		Name:      in.WorkflowType,
		Tags:      map[string]string{workflowIDTagKey: in.Options.ID},
	}, t.root.headerWriter(t.root.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.SignalWithStartWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) QueryWorkflow(
	ctx context.Context,
	in *interceptor.ClientQueryWorkflowInput,
) (val converter.EncodedValue, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "QueryWorkflow",
		Name:      in.QueryType,
		Tags:      map[string]string{workflowIDTagKey: in.WorkflowID},
	}, t.root.headerWriter(t.root.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.QueryWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) UpdateWorkflow(
	ctx context.Context,
	in *interceptor.ClientUpdateWorkflowInput,
) (val client.WorkflowUpdateHandle, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "StartWorkflowUpdate",
		Name:      in.UpdateName,
		Tags: map[string]string{
			workflowIDTagKey: in.WorkflowID,
			updateIDTagKey:   in.UpdateID,
		},
	}, t.root.headerWriter(t.root.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.UpdateWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) UpdateWithStartWorkflow(
	ctx context.Context,
	in *interceptor.ClientUpdateWithStartWorkflowInput,
) (val client.WorkflowUpdateHandle, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "UpdateWithStartWorkflow",
		Name:      in.UpdateOptions.UpdateName,
		Tags:      map[string]string{workflowIDTagKey: in.UpdateOptions.WorkflowID, updateIDTagKey: in.UpdateOptions.UpdateID},
	}, t.root.headerWriter(t.root.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.UpdateWithStartWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ClientExecuteActivityInput,
) (handle client.ActivityHandle, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "StartActivity",
		Name:      in.ActivityType,
		Tags:      map[string]string{activityIDTagKey: in.Options.ID},
	}, t.root.headerWriter(t.root.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteActivity(ctx, in)
}

func (t *tracingClientOutboundInterceptor) ExecuteNexusOperation(
	ctx context.Context,
	in *interceptor.ClientExecuteNexusOperationInput,
) (handle client.NexusOperationHandle, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "StartNexusOperation",
		Name:      in.Service + "/" + in.OperationType,
		Tags:      nexusTags(in.Endpoint, in.Service, in.OperationType),
	}, t.root.nexusHeaderWriter(t.root.tracer, in.NexusHeader))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteNexusOperation(ctx, in)
}

func (t *tracingClientOutboundInterceptor) CancelWorkflow(
	ctx context.Context,
	in *interceptor.ClientCancelWorkflowInput,
) (err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "CancelWorkflow",
		Tags:      workflowExecutionTags(in.WorkflowID, in.RunID),
	}, nil)
	if err != nil {
		return err
	}
	defer endSpan(&err)

	return t.Next.CancelWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) TerminateWorkflow(
	ctx context.Context,
	in *interceptor.ClientTerminateWorkflowInput,
) (err error) {
	tags := workflowExecutionTags(in.WorkflowID, in.RunID)
	if in.Reason != "" {
		tags[terminateReasonTagKey] = in.Reason
	}
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "TerminateWorkflow",
		Tags:      tags,
	}, nil)
	if err != nil {
		return err
	}
	defer endSpan(&err)

	return t.Next.TerminateWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) DescribeWorkflow(
	ctx context.Context,
	in *interceptor.ClientDescribeWorkflowInput,
) (out *interceptor.ClientDescribeWorkflowOutput, err error) {
	ctx, endSpan, err := startOutboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation: "DescribeWorkflow",
		Tags:      workflowExecutionTags(in.WorkflowID, in.RunID),
	}, nil)
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.DescribeWorkflow(ctx, in)
}

type tracingActivityOutboundInterceptor struct {
	interceptor.ActivityOutboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingActivityOutboundInterceptor) GetLogger(ctx context.Context) log.Logger {
	if span := t.root.tracer.SpanFromContext(ctx); span != nil {
		return t.root.tracer.GetLogger(t.Next.GetLogger(ctx), span)
	}
	return t.Next.GetLogger(ctx)
}

type tracingActivityInboundInterceptor struct {
	interceptor.ActivityInboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingActivityInboundInterceptor) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	i := &tracingActivityOutboundInterceptor{root: t.root}
	i.Next = outbound
	return t.Next.Init(i)
}

func (t *tracingActivityInboundInterceptor) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ExecuteActivityInput,
) (ret interface{}, err error) {
	info := activity.GetInfo(ctx)
	ctx, endSpan, err := startInboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation:  "RunActivity",
		Name:       info.ActivityType.Name,
		DependedOn: true,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
			activityIDTagKey: info.ActivityID,
		},
	}, t.root.headerReader(t.root.tracer, ctx))
	if err != nil {
		return nil, err
	}

	var spanErr error
	defer endSpan(&spanErr)
	ret, err = t.Next.ExecuteActivity(ctx, in)
	if err != activity.ErrResultPending {
		spanErr = err
	}
	return ret, err
}

type tracingWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingWorkflowInboundInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	i := &tracingWorkflowOutboundInterceptor{root: t.root}
	i.Next = outbound
	return t.Next.Init(i)
}

func (t *tracingWorkflowInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (ret interface{}, err error) {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "RunWorkflow",
		Name:      info.WorkflowType.Name,
		Tags:      workflowTags(info),
	}, t.root.workflowHeaderReader(t.root.workflowTracer, ctx))
	if err != nil {
		return nil, err
	}

	var spanErr error
	defer endSpan(&spanErr)
	ret, err = t.Next.ExecuteWorkflow(ctx, in)
	if !isContinueAsNewError(err) {
		spanErr = err
	}
	return ret, err
}

func (t *tracingWorkflowInboundInterceptor) HandleSignal(ctx workflow.Context, in *interceptor.HandleSignalInput) (err error) {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "HandleSignal",
		Name:      in.SignalName,
		Tags:      workflowTags(info),
	}, t.root.workflowHeaderReader(t.root.workflowTracer, ctx))
	if err != nil {
		return err
	}
	defer endSpan(&err)

	return t.Next.HandleSignal(ctx, in)
}

func (t *tracingWorkflowInboundInterceptor) HandleQuery(
	ctx workflow.Context,
	in *interceptor.HandleQueryInput,
) (val interface{}, err error) {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "HandleQuery",
		Name:      in.QueryType,
		Tags:      workflowTags(info),
	}, t.root.workflowHeaderReader(t.root.workflowTracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.HandleQuery(ctx, in)
}

func (t *tracingWorkflowInboundInterceptor) ValidateUpdate(
	ctx workflow.Context,
	in *interceptor.UpdateInput,
) (err error) {
	info := workflow.GetInfo(ctx)
	currentUpdateInfo := workflow.GetCurrentUpdateInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "ValidateUpdate",
		Name:      in.Name,
		Tags:      workflowTagsWithUpdate(info, currentUpdateInfo.ID),
	}, t.root.workflowHeaderReader(t.root.workflowTracer, ctx))
	if err != nil {
		return err
	}
	defer endSpan(&err)

	return t.Next.ValidateUpdate(ctx, in)
}

func (t *tracingWorkflowInboundInterceptor) ExecuteUpdate(
	ctx workflow.Context,
	in *interceptor.UpdateInput,
) (val interface{}, err error) {
	info := workflow.GetInfo(ctx)
	currentUpdateInfo := workflow.GetCurrentUpdateInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "HandleUpdate",
		Name:      in.Name,
		Tags:      workflowTagsWithUpdate(info, currentUpdateInfo.ID),
	}, t.root.workflowHeaderReader(t.root.workflowTracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteUpdate(ctx, in)
}

type tracingWorkflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startOutboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation:  "StartActivity",
		Name:       activityType,
		Tags:       workflowTags(info),
		DependedOn: true,
	}, t.root.workflowHeaderWriter(t.root.workflowTracer, ctx))
	if err != nil {
		return workflowFutureFromErr(ctx, err)
	}
	defer endSpan(nil)

	return t.Next.ExecuteActivity(ctx, activityType, args...)
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteLocalActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startOutboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation:  "StartActivity",
		Name:       activityType,
		Tags:       workflowTags(info),
		DependedOn: true,
	}, t.root.workflowHeaderWriter(t.root.workflowTracer, ctx))
	if err != nil {
		return workflowFutureFromErr(ctx, err)
	}
	defer endSpan(nil)

	return t.Next.ExecuteLocalActivity(ctx, activityType, args...)
}

func (t *tracingWorkflowOutboundInterceptor) GetLogger(ctx workflow.Context) log.Logger {
	if span := t.root.workflowTracer.SpanFromContext(ctx); span != nil {
		return t.root.workflowTracer.GetLogger(t.Next.GetLogger(ctx), span)
	}
	return t.Next.GetLogger(ctx)
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteChildWorkflow(
	ctx workflow.Context,
	childWorkflowType string,
	args ...interface{},
) workflow.ChildWorkflowFuture {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startOutboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "StartChildWorkflow",
		Name:      childWorkflowType,
		Tags:      workflowTags(info),
	}, t.root.workflowHeaderWriter(t.root.workflowTracer, ctx))
	if err != nil {
		return childWorkflowFuture{workflowFutureFromErr(ctx, err)}
	}
	defer endSpan(nil)

	return t.Next.ExecuteChildWorkflow(ctx, childWorkflowType, args...)
}

func (t *tracingWorkflowOutboundInterceptor) SignalExternalWorkflow(
	ctx workflow.Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) workflow.Future {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startOutboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "SignalExternalWorkflow",
		Name:      signalName,
		Tags:      workflowTags(info),
	}, t.root.workflowHeaderWriter(t.root.workflowTracer, ctx))
	if err != nil {
		return workflowFutureFromErr(ctx, err)
	}
	defer endSpan(nil)

	return t.Next.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (t *tracingWorkflowOutboundInterceptor) SignalChildWorkflow(
	ctx workflow.Context,
	workflowID string,
	signalName string,
	arg interface{},
) workflow.Future {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startOutboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "SignalChildWorkflow",
		Name:      signalName,
		Tags:      workflowTags(info),
	}, t.root.workflowHeaderWriter(t.root.workflowTracer, ctx))
	if err != nil {
		return workflowFutureFromErr(ctx, err)
	}
	defer endSpan(nil)

	return t.Next.SignalChildWorkflow(ctx, workflowID, signalName, arg)
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteNexusOperation(ctx workflow.Context, input interceptor.ExecuteNexusOperationInput) workflow.NexusOperationFuture {
	var ok bool
	var operationName string
	if operationName, ok = input.Operation.(string); ok {
	} else if regOp, ok := input.Operation.(interface{ Name() string }); ok {
		operationName = regOp.Name()
	} else {
		return nexusOperationFuture{workflowFutureFromErr(ctx, fmt.Errorf("unexpected operation type: %v", input.Operation))}
	}
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startOutboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "StartNexusOperation",
		Name:      input.Client.Service() + "/" + operationName,
		Tags:      workflowTagsWithNexus(info, input.Client.Endpoint(), input.Client.Service(), operationName),
	}, t.root.nexusHeaderWriter(t.root.workflowTracer, input.NexusHeader))
	if err != nil {
		return nexusOperationFuture{workflowFutureFromErr(ctx, err)}
	}
	defer endSpan(nil)

	return t.Next.ExecuteNexusOperation(ctx, input)
}

func (t *tracingWorkflowOutboundInterceptor) NewContinueAsNewError(
	ctx workflow.Context,
	wfn interface{},
	args ...interface{},
) error {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startOutboundWorkflowSpan(t.root.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "ContinueAsNew",
		Name:      info.WorkflowType.Name,
		Tags:      workflowTags(info),
	}, t.root.workflowHeaderWriter(t.root.workflowTracer, ctx))
	if err != nil {
		return err
	}

	var spanErr error
	defer endSpan(&spanErr)
	err = t.Next.NewContinueAsNewError(ctx, wfn, args...)
	if !isContinueAsNewError(err) {
		spanErr = err
	}
	return err
}

type tracingNexusOperationInboundInterceptor struct {
	interceptor.NexusOperationInboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingNexusOperationInboundInterceptor) Init(
	ctx context.Context,
	outbound interceptor.NexusOperationOutboundInterceptor,
) error {
	i := &tracingNexusOperationOutboundInterceptor{root: t.root}
	i.Next = outbound
	return t.Next.Init(ctx, i)
}

type tracingNexusOperationOutboundInterceptor struct {
	interceptor.NexusOperationOutboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingNexusOperationOutboundInterceptor) GetLogger(ctx context.Context) log.Logger {
	if span := t.root.tracer.SpanFromContext(ctx); span != nil {
		return t.root.tracer.GetLogger(t.Next.GetLogger(ctx), span)
	}
	return t.Next.GetLogger(ctx)
}

func (t *tracingNexusOperationInboundInterceptor) CancelOperation(ctx context.Context, input interceptor.NexusCancelOperationInput) (err error) {
	info := nexus.ExtractHandlerInfo(ctx)
	ctx, endSpan, err := startInboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation:  "RunCancelNexusOperationHandler",
		Name:       info.Service + "/" + info.Operation,
		DependedOn: true,
		Tags:       nexusTags("", info.Service, info.Operation),
	}, t.root.nexusHeaderReader(t.root.tracer, input.Options.Header))
	if err != nil {
		return err
	}
	defer endSpan(&err)

	return t.Next.CancelOperation(ctx, input)
}

func (t *tracingNexusOperationInboundInterceptor) StartOperation(ctx context.Context, input interceptor.NexusStartOperationInput) (ret nexus.HandlerStartOperationResult[any], err error) {
	info := nexus.ExtractHandlerInfo(ctx)
	ctx, endSpan, err := startInboundSpan(t.root.tracer, ctx, &TracerStartSpanOptions{
		Operation:  "RunStartNexusOperationHandler",
		Name:       info.Service + "/" + info.Operation,
		DependedOn: true,
		Tags:       nexusTags("", info.Service, info.Operation),
	}, t.root.nexusHeaderReader(t.root.tracer, input.Options.Header))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.StartOperation(ctx, input)
}

func (t *tracingInterceptor) headerReader(tracer Tracer, ctx context.Context) func() (TracerSpanRef, error) {
	header := interceptor.Header(ctx)
	return func() (TracerSpanRef, error) {
		return t.readSpanFromHeader(tracer, header)
	}
}

func (t *tracingInterceptor) headerWriter(tracer Tracer, ctx context.Context) func(TracerSpanRef) error {
	header := interceptor.Header(ctx)
	return func(span TracerSpanRef) error {
		return t.writeSpanToHeader(tracer, span, header)
	}
}

func (t *tracingInterceptor) workflowHeaderReader(tracer WorkflowTracer, ctx workflow.Context) func() (TracerSpanRef, error) {
	header := interceptor.WorkflowHeader(ctx)
	return func() (TracerSpanRef, error) {
		return t.readSpanFromHeader(tracer, header)
	}
}

func (t *tracingInterceptor) workflowHeaderWriter(tracer WorkflowTracer, ctx workflow.Context) func(TracerSpanRef) error {
	header := interceptor.WorkflowHeader(ctx)
	return func(span TracerSpanRef) error {
		return t.writeSpanToHeader(tracer, span, header)
	}
}

func (t *tracingInterceptor) nexusHeaderReader(tracer tracerCommon, header nexus.Header) func() (TracerSpanRef, error) {
	return func() (TracerSpanRef, error) {
		return t.readSpanFromNexusHeader(tracer, header)
	}
}

func (t *tracingInterceptor) nexusHeaderWriter(tracer tracerCommon, header nexus.Header) func(TracerSpanRef) error {
	return func(span TracerSpanRef) error {
		return t.writeSpanToNexusHeader(tracer, span, header)
	}
}

func (t *tracingInterceptor) readSpanFromHeader(tracer tracerCommon, header map[string]*commonpb.Payload) (TracerSpanRef, error) {
	payload := header[tracer.Options().HeaderKey]
	if payload == nil {
		return nil, nil
	}
	var data map[string]string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &data); err != nil {
		return nil, err
	}
	return tracer.UnmarshalSpan(data)
}

func (t *tracingInterceptor) writeSpanToHeader(tracer tracerCommon, span TracerSpanRef, header map[string]*commonpb.Payload) error {
	data, err := tracer.MarshalSpan(span)
	if err != nil || len(data) == 0 {
		return err
	}
	payload, err := converter.GetDefaultDataConverter().ToPayload(data)
	if err != nil {
		return err
	}
	header[tracer.Options().HeaderKey] = payload
	return nil
}

func (t *tracingInterceptor) writeSpanToNexusHeader(tracer tracerCommon, span TracerSpanRef, header nexus.Header) error {
	data, err := tracer.MarshalSpan(span)
	if err != nil || len(data) == 0 {
		return err
	}
	for k, v := range data {
		header.Set(k, v)
	}
	return nil
}

func (t *tracingInterceptor) readSpanFromNexusHeader(tracer tracerCommon, header nexus.Header) (TracerSpanRef, error) {
	return tracer.UnmarshalSpan(header)
}

func nexusTags(endpoint, service, operation string) map[string]string {
	tags := map[string]string{
		nexusServiceTagKey:   service,
		nexusOperationTagKey: operation,
	}
	if endpoint != "" {
		tags[nexusEndpointTagKey] = endpoint
	}
	return tags
}

func workflowExecutionTags(workflowID, runID string) map[string]string {
	tags := map[string]string{workflowIDTagKey: workflowID}
	if runID != "" {
		tags[runIDTagKey] = runID
	}
	return tags
}

func workflowTags(info *workflow.Info) map[string]string {
	return workflowExecutionTags(info.WorkflowExecution.ID, info.WorkflowExecution.RunID)
}

func workflowTagsWithUpdate(info *workflow.Info, updateID string) map[string]string {
	tags := workflowTags(info)
	tags[updateIDTagKey] = updateID
	return tags
}

func workflowTagsWithNexus(info *workflow.Info, endpoint, service, operation string) map[string]string {
	tags := workflowTags(info)
	tags[nexusServiceTagKey] = service
	tags[nexusOperationTagKey] = operation
	if endpoint != "" {
		tags[nexusEndpointTagKey] = endpoint
	}
	return tags
}

func startInboundSpan(
	t Tracer,
	ctx context.Context,
	options *TracerStartSpanOptions,
	headerReader func() (TracerSpanRef, error),
) (context.Context, func(err *error), error) {
	createSpan := t.Options().AddTemporalSpans

	curr, err := parentFromHeader(t, headerReader)
	if err != nil {
		return ctx, nil, err
	}

	// If there is no span in the headers, use the current span from the context.
	if curr == nil {
		curr = t.SpanFromContext(ctx)
	}

	if createSpan {
		options.Direction = SpanDirectionInbound
		options.Parent = curr
		curr = t.CreateSpan(ctx, options)
	}

	ctx = t.ContextWithSpan(ctx, curr)

	return ctx, finishSpan(curr, createSpan), nil
}

func startInboundWorkflowSpan(
	t WorkflowTracer,
	ctx workflow.Context,
	options *TracerStartSpanOptions,
	headerReader func() (TracerSpanRef, error),
) (workflow.Context, func(err *error), error) {
	createSpan := t.Options().AddTemporalSpans

	curr, err := parentFromHeader(t, headerReader)
	if err != nil {
		return ctx, nil, err
	}

	// If there is no span in the headers, use the current span from the context.
	if curr == nil {
		curr = t.SpanFromContext(ctx)
	}

	if createSpan {
		options.Direction = SpanDirectionInbound
		options.Parent = curr
		curr = t.CreateSpan(ctx, options)
	}

	ctx = t.ContextWithSpan(ctx, curr)

	return ctx, finishSpan(curr, createSpan), nil
}

func startOutboundSpan(
	t Tracer,
	ctx context.Context,
	options *TracerStartSpanOptions,
	headerWriter func(TracerSpanRef) error,
) (context.Context, func(err *error), error) {
	createSpan := t.Options().AddTemporalSpans

	curr := t.SpanFromContext(ctx)

	if createSpan {
		options.Direction = SpanDirectionOutbound
		options.Parent = curr
		curr = t.CreateSpan(ctx, options)
		ctx = t.ContextWithSpan(ctx, curr)
	}

	finish, err := writeSpanHeader(curr, createSpan, headerWriter)
	return ctx, finish, err
}

func startOutboundWorkflowSpan(
	t WorkflowTracer,
	ctx workflow.Context,
	options *TracerStartSpanOptions,
	headerWriter func(TracerSpanRef) error,
) (workflow.Context, func(err *error), error) {
	createSpan := t.Options().AddTemporalSpans

	curr := t.SpanFromContext(ctx)

	if createSpan {
		options.Direction = SpanDirectionOutbound
		options.Parent = curr
		curr = t.CreateSpan(ctx, options)
		ctx = t.ContextWithSpan(ctx, curr)
	}

	finish, err := writeSpanHeader(curr, createSpan, headerWriter)
	return ctx, finish, err
}

func isContinueAsNewError(err error) bool {
	var continueAsNewErr *workflow.ContinueAsNewError
	return errors.As(err, &continueAsNewErr)
}

func parentFromHeader(t tracerCommon, read func() (TracerSpanRef, error)) (TracerSpanRef, error) {
	span, err := read()
	if err != nil && !t.Options().AllowInvalidParentSpans {
		return nil, err
	}
	return span, nil
}

func finishSpan(span TracerSpanRef, created bool) func(err *error) {
	if !created {
		return func(err *error) {}
	}

	if span, ok := span.(TracerSpan); ok {
		return func(err *error) {
			opts := &TracerFinishSpanOptions{}
			if err != nil {
				opts.Error = *err
			}
			span.Finish(opts)
		}
	}

	return func(err *error) {}
}

func writeSpanHeader(
	span TracerSpanRef,
	created bool,
	headerWriter func(TracerSpanRef) error,
) (func(err *error), error) {
	finish := finishSpan(span, created)

	if headerWriter == nil {
		return finish, nil
	}

	if err := headerWriter(span); err != nil {
		finish(&err)
		return nil, err
	}

	return finish, nil
}

func workflowFutureFromErr(ctx workflow.Context, err error) workflow.Future {
	fut, set := workflow.NewFuture(ctx)
	set.SetError(err)
	return fut
}

type nexusOperationFuture struct{ workflow.Future }

func (e nexusOperationFuture) GetNexusOperationExecution() workflow.Future { return e }

type childWorkflowFuture struct{ workflow.Future }

func (e childWorkflowFuture) GetChildWorkflowExecution() workflow.Future { return e }

func (e childWorkflowFuture) SignalChildWorkflow(ctx workflow.Context, signalName string, data interface{}) workflow.Future {
	return e
}
