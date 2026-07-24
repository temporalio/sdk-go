package tracing

import (
	"context"
	"fmt"
	"time"

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
	workflowIDTagKey = "temporalWorkflowID"
	runIDTagKey      = "temporalRunID"
	activityIDTagKey = "temporalActivityID"
	updateIDTagKey   = "temporalUpdateID"
)

// tracerCommon is the context-independent surface shared by Tracer and
// WorkflowTracer.
type tracerCommon interface {
	Options() TracerOptions
	UnmarshalSpan(map[string]string) (TracerSpanRef, error)
	// MarshalSpan marshals a span into a map. An empty map with no error means
	// the span is not set on the header.
	MarshalSpan(TracerSpan) (map[string]string, error)
	// GetLogger may add fields that correlate logs with traces.
	GetLogger(log.Logger, TracerSpanRef) log.Logger
	// SpanName names a span from options, or defer to BaseTracer.
	SpanName(options *TracerStartSpanOptions) string
	mustEmbedBaseTracer()
}

// Tracer traces client, activity, and Nexus operations on context.Context.
// Workflow operations use WorkflowTracer. Most callers should use a contrib
// integration such as go.temporal.io/sdk/contrib/opentelemetry-v2 instead.
//
// All implementations must embed BaseTracer to safely handle future changes.
type Tracer interface {
	tracerCommon

	SpanFromContext(context.Context) TracerSpan
	ContextWithSpan(context.Context, TracerSpan) context.Context

	// CreateSpan creates a span whose parent is options.Parent (or none).
	// Implementations must not fail the Temporal operation for tracing problems;
	// treat a missing or unrecognized parent as no parent and return a span
	// (possibly no-op).
	CreateSpan(context.Context, *TracerStartSpanOptions) TracerSpan
}

// WorkflowTracer traces workflow operations on workflow.Context. Implementors
// that support deterministic IDs should honor TracerStartSpanOptions.Unsequenced
// (skip the id stream) and suppress Finish during workflow.IsReplaying for
// sequenced spans so wall-clock end times are not shifted on replay.
// Application code creating custom workflow spans should use the vendor helper
// (e.g. opentelemetry-v2.NewTracer) rather than this interface.
//
// All implementations must embed BaseTracer to safely handle future changes.
type WorkflowTracer interface {
	tracerCommon

	SpanFromContext(workflow.Context) TracerSpan
	ContextWithSpan(workflow.Context, TracerSpan) workflow.Context

	// CreateSpan creates a span whose parent is options.Parent (or none). Same
	// rules as Tracer.CreateSpan. When Unsequenced is false, prefer deterministic
	// IDs and a Finish that is a no-op during replay.
	CreateSpan(workflow.Context, *TracerStartSpanOptions) TracerSpan
}

// BaseTracer is a default Tracer/WorkflowTracer implementation meant for embedding.
type BaseTracer struct{}

func (BaseTracer) GetLogger(logger log.Logger, ref TracerSpanRef) log.Logger {
	return logger
}
func (BaseTracer) SpanName(options *TracerStartSpanOptions) string {
	if options.Operation == "" {
		return options.Name
	}
	return fmt.Sprintf("%s:%s", options.Operation, options.Name)
}

//lint:ignore U1000 Ignore unused method; it is only required to implement the Tracer interface but will never be called.
func (BaseTracer) mustEmbedBaseTracer() {}

// TracerOptions are options returned from Tracer.Options.
type TracerOptions struct {
	// HeaderKey is the Temporal header key used to serialize the span. Must not
	// be empty.
	HeaderKey string

	// DisableSignalTracing disables signal tracing.
	DisableSignalTracing bool

	// DisableQueryTracing disables query tracing.
	DisableQueryTracing bool

	// DisableUpdateTracing disables update tracing.
	DisableUpdateTracing bool

	// AllowInvalidParentSpans swallows errors interpreting parent spans from
	// headers. Useful when migrating tracing libraries while workflows/activities
	// may be in progress.
	AllowInvalidParentSpans bool
}

// SpanDirection is whether a span is for an inbound or outbound interceptor.
type SpanDirection int

const (
	SpanDirectionUnspecified SpanDirection = iota
	SpanDirectionInbound
	SpanDirectionOutbound
)

// TracerStartSpanOptions are options for Tracer/WorkflowTracer.CreateSpan.
type TracerStartSpanOptions struct {
	// Parent is the optional parent. The interceptor sets this from the header
	// for inbound spans and from the context for outbound spans; nil means no
	// parent.
	Parent TracerSpanRef

	// Operation is the general operation name (e.g. "RunWorkflow").
	Operation string

	// Name is the specific workflow, activity, etc. for the operation.
	Name string

	// Time is the span start time. For RunWorkflow and RunActivity this matches
	// WorkflowStartTime / StartedTime. Other workflow-path operations should use
	// workflow.Now so start times stay deterministic across replay; non-workflow
	// and Unsequenced operations typically use wall-clock time.Now().
	Time time.Time

	// DependedOn is true if the parent depends on this span (OpenTracing
	// ChildOf), false if only related (FollowsFrom).
	DependedOn bool

	// Direction is inbound or outbound. Tracers may use this for span kind.
	Direction SpanDirection

	// Tags are span tags.
	Tags map[string]string

	// Unsequenced opts out of deterministic span IDs and replay-safe Finish.
	// Set for operations not recorded in workflow history (HandleQuery,
	// ValidateUpdate): those may run any number of times and must not advance
	// the workflow tracer's id stream or rely on IsReplaying to suppress End.
	Unsequenced bool
}

// TracerSpanRef is a span reference such as a parent.
type TracerSpanRef interface {
}

// TracerSpan is a started span.
type TracerSpan interface {
	TracerSpanRef
	Finish(*TracerFinishSpanOptions)
}

// TracerFinishSpanOptions are options for TracerSpan.Finish.
type TracerFinishSpanOptions struct {
	// Error is set if the traced code failed.
	Error error
}

type TracerFactory func() Tracer

type WorkflowTracerFactory func() WorkflowTracer

type tracingInterceptor struct {
	interceptor.InterceptorBase
	newTracer         TracerFactory
	newWorkflowTracer WorkflowTracerFactory
}

// NewTracingInterceptor creates a tracing interceptor from the given factories.
// Most callers should use a contrib integration such as
// go.temporal.io/sdk/contrib/opentelemetry-v2 instead.
func NewTracingInterceptor(newTracer TracerFactory, newWorkflowTracer WorkflowTracerFactory) interceptor.Interceptor {
	return &tracingInterceptor{newTracer: newTracer, newWorkflowTracer: newWorkflowTracer}
}

func (t *tracingInterceptor) InterceptClient(next interceptor.ClientOutboundInterceptor) interceptor.ClientOutboundInterceptor {
	i := &tracingClientOutboundInterceptor{root: t, tracer: t.newTracer()}
	i.Next = next
	return i
}

func (t *tracingInterceptor) InterceptActivity(
	ctx context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	i := &tracingActivityInboundInterceptor{root: t, tracer: t.newTracer()}
	i.Next = next
	return i
}

func (t *tracingInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	i := &tracingWorkflowInboundInterceptor{
		root:           t,
		workflowTracer: t.newWorkflowTracer(),
	}
	i.Next = next
	return i
}

func (t *tracingInterceptor) InterceptNexusOperation(
	ctx context.Context,
	next interceptor.NexusOperationInboundInterceptor,
) interceptor.NexusOperationInboundInterceptor {
	i := &tracingNexusOperationInboundInterceptor{root: t, tracer: t.newTracer()}
	i.Next = next
	return i
}

type tracingClientOutboundInterceptor struct {
	interceptor.ClientOutboundInterceptorBase
	root   *tracingInterceptor
	tracer Tracer
}

func (t *tracingClientOutboundInterceptor) CreateSchedule(ctx context.Context, in *interceptor.ScheduleClientCreateInput) (run client.ScheduleHandle, err error) {
	endSpan, err := startOutboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "CreateSchedule",
		Name:      in.Options.ID,
	}, t.root.headerWriter(t.tracer, ctx))
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
	endSpan, err := startOutboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "StartWorkflow",
		Name:      in.WorkflowType,
		Tags:      map[string]string{workflowIDTagKey: in.Options.ID},
	}, t.root.headerWriter(t.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteWorkflow(ctx, in)
}

func (t *tracingClientOutboundInterceptor) SignalWorkflow(ctx context.Context, in *interceptor.ClientSignalWorkflowInput) (err error) {
	if t.tracer.Options().DisableSignalTracing {
		return t.Next.SignalWorkflow(ctx, in)
	}
	endSpan, err := startOutboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "SignalWorkflow",
		Name:      in.SignalName,
		Tags:      map[string]string{workflowIDTagKey: in.WorkflowID},
	}, t.root.headerWriter(t.tracer, ctx))
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
	endSpan, err := startOutboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "SignalWithStartWorkflow",
		Name:      in.WorkflowType,
		Tags:      map[string]string{workflowIDTagKey: in.Options.ID},
	}, t.root.headerWriter(t.tracer, ctx))
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
	if t.tracer.Options().DisableQueryTracing {
		return t.Next.QueryWorkflow(ctx, in)
	}
	endSpan, err := startOutboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "QueryWorkflow",
		Name:      in.QueryType,
		Tags:      map[string]string{workflowIDTagKey: in.WorkflowID},
	}, t.root.headerWriter(t.tracer, ctx))
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
	if t.tracer.Options().DisableUpdateTracing {
		return t.Next.UpdateWorkflow(ctx, in)
	}
	endSpan, err := startOutboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "UpdateWorkflow",
		Name:      in.UpdateName,
		Tags:      map[string]string{workflowIDTagKey: in.WorkflowID},
	}, t.root.headerWriter(t.tracer, ctx))
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
	if t.tracer.Options().DisableUpdateTracing {
		return t.Next.UpdateWithStartWorkflow(ctx, in)
	}
	endSpan, err := startOutboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "UpdateWithStartWorkflow",
		Name:      in.UpdateOptions.UpdateName,
		Tags:      map[string]string{workflowIDTagKey: in.UpdateOptions.WorkflowID, updateIDTagKey: in.UpdateOptions.UpdateID},
	}, t.root.headerWriter(t.tracer, ctx))
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
	endSpan, err := startOutboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "StartActivity",
		Name:      in.ActivityType,
		Tags:      map[string]string{activityIDTagKey: in.Options.ID},
	}, t.root.headerWriter(t.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteActivity(ctx, in)
}

type tracingActivityOutboundInterceptor struct {
	interceptor.ActivityOutboundInterceptorBase
	root   *tracingInterceptor
	tracer Tracer
}

func (t *tracingActivityOutboundInterceptor) GetLogger(ctx context.Context) log.Logger {
	if span := t.tracer.SpanFromContext(ctx); span != nil {
		return t.tracer.GetLogger(t.Next.GetLogger(ctx), span)
	}
	return t.Next.GetLogger(ctx)
}

type tracingActivityInboundInterceptor struct {
	interceptor.ActivityInboundInterceptorBase
	root   *tracingInterceptor
	tracer Tracer
}

func (t *tracingActivityInboundInterceptor) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	i := &tracingActivityOutboundInterceptor{root: t.root, tracer: t.tracer}
	i.Next = outbound
	return t.Next.Init(i)
}

func (t *tracingActivityInboundInterceptor) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ExecuteActivityInput,
) (ret interface{}, err error) {
	info := activity.GetInfo(ctx)
	ctx, endSpan, err := startInboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation:  "RunActivity",
		Name:       info.ActivityType.Name,
		DependedOn: true,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
			activityIDTagKey: info.ActivityID,
		},
		Time: info.StartedTime,
	}, t.root.headerReader(t.tracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteActivity(ctx, in)
}

func workflowTags(info *workflow.Info) map[string]string {
	return map[string]string{
		workflowIDTagKey: info.WorkflowExecution.ID,
		runIDTagKey:      info.WorkflowExecution.RunID,
	}
}

func workflowTagsWithUpdate(info *workflow.Info, updateID string) map[string]string {
	tags := workflowTags(info)
	tags[updateIDTagKey] = updateID
	return tags
}

type tracingWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	root           *tracingInterceptor
	workflowTracer WorkflowTracer
}

func (t *tracingWorkflowInboundInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	i := &tracingWorkflowOutboundInterceptor{root: t.root, tracer: t.workflowTracer}
	i.Next = outbound
	return t.Next.Init(i)
}

func (t *tracingWorkflowInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (ret interface{}, err error) {
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "RunWorkflow",
		Name:      info.WorkflowType.Name,
		Tags:      workflowTags(info),
		Time:      info.WorkflowStartTime,
	}, t.root.workflowHeaderReader(t.workflowTracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteWorkflow(ctx, in)
}

func (t *tracingWorkflowInboundInterceptor) HandleSignal(ctx workflow.Context, in *interceptor.HandleSignalInput) (err error) {
	if t.workflowTracer.Options().DisableSignalTracing {
		return t.Next.HandleSignal(ctx, in)
	}
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "HandleSignal",
		Name:      in.SignalName,
		Tags:      workflowTags(info),
		Time:      workflow.Now(ctx),
	}, t.root.workflowHeaderReader(t.workflowTracer, ctx))
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
	if t.workflowTracer.Options().DisableQueryTracing {
		return t.Next.HandleQuery(ctx, in)
	}
	// Queries are not in workflow history and may run repeatedly; do not consume
	// the deterministic id stream or suppress Finish on replay.
	info := workflow.GetInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation:   "HandleQuery",
		Name:        in.QueryType,
		Tags:        workflowTags(info),
		Unsequenced: true,
	}, t.root.workflowHeaderReader(t.workflowTracer, ctx))
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
	if t.workflowTracer.Options().DisableUpdateTracing {
		return t.Next.ValidateUpdate(ctx, in)
	}
	// Update validation is not recorded in history and is not replayed; same
	// Unsequenced rules as HandleQuery.
	info := workflow.GetInfo(ctx)
	currentUpdateInfo := workflow.GetCurrentUpdateInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation:   "ValidateUpdate",
		Name:        in.Name,
		Tags:        workflowTagsWithUpdate(info, currentUpdateInfo.ID),
		Unsequenced: true,
	}, t.root.workflowHeaderReader(t.workflowTracer, ctx))
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
	if t.workflowTracer.Options().DisableUpdateTracing {
		return t.Next.ExecuteUpdate(ctx, in)
	}
	info := workflow.GetInfo(ctx)
	currentUpdateInfo := workflow.GetCurrentUpdateInfo(ctx)
	ctx, endSpan, err := startInboundWorkflowSpan(t.workflowTracer, ctx, &TracerStartSpanOptions{
		Operation: "HandleUpdate",
		Name:      in.Name,
		Tags:      workflowTagsWithUpdate(info, currentUpdateInfo.ID),
		Time:      workflow.Now(ctx),
	}, t.root.workflowHeaderReader(t.workflowTracer, ctx))
	if err != nil {
		return nil, err
	}
	defer endSpan(&err)

	return t.Next.ExecuteUpdate(ctx, in)
}

type tracingWorkflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	root   *tracingInterceptor
	tracer WorkflowTracer
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	info := workflow.GetInfo(ctx)
	endSpan, err := startOutboundWorkflowSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation:  "StartActivity",
		Name:       activityType,
		Tags:       workflowTags(info),
		DependedOn: true,
		Time:       workflow.Now(ctx),
	}, t.root.workflowHeaderWriter(t.tracer, ctx))
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
	endSpan, err := startOutboundWorkflowSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation:  "StartActivity",
		Name:       activityType,
		Tags:       workflowTags(info),
		DependedOn: true,
		Time:       workflow.Now(ctx),
	}, t.root.workflowHeaderWriter(t.tracer, ctx))
	if err != nil {
		return workflowFutureFromErr(ctx, err)
	}
	defer endSpan(nil)

	return t.Next.ExecuteLocalActivity(ctx, activityType, args...)
}

func (t *tracingWorkflowOutboundInterceptor) GetLogger(ctx workflow.Context) log.Logger {
	if span := t.tracer.SpanFromContext(ctx); span != nil {
		return t.tracer.GetLogger(t.Next.GetLogger(ctx), span)
	}
	return t.Next.GetLogger(ctx)
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteChildWorkflow(
	ctx workflow.Context,
	childWorkflowType string,
	args ...interface{},
) workflow.ChildWorkflowFuture {
	info := workflow.GetInfo(ctx)
	endSpan, err := startOutboundWorkflowSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "StartChildWorkflow",
		Name:      childWorkflowType,
		Tags:      workflowTags(info),
		Time:      workflow.Now(ctx),
	}, t.root.workflowHeaderWriter(t.tracer, ctx))
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
	if t.tracer.Options().DisableSignalTracing {
		return t.Next.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
	}
	info := workflow.GetInfo(ctx)
	endSpan, err := startOutboundWorkflowSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "SignalExternalWorkflow",
		Name:      signalName,
		Tags:      workflowTags(info),
		Time:      workflow.Now(ctx),
	}, t.root.workflowHeaderWriter(t.tracer, ctx))
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
	if t.tracer.Options().DisableSignalTracing {
		return t.Next.SignalChildWorkflow(ctx, workflowID, signalName, arg)
	}

	info := workflow.GetInfo(ctx)
	endSpan, err := startOutboundWorkflowSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "SignalChildWorkflow",
		Name:      signalName,
		Tags:      workflowTags(info),
		Time:      workflow.Now(ctx),
	}, t.root.workflowHeaderWriter(t.tracer, ctx))
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
	endSpan, err := startOutboundWorkflowSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "StartNexusOperation",
		Name:      input.Client.Service() + "/" + operationName,
		Tags:      workflowTags(info),
		Time:      workflow.Now(ctx),
	}, t.root.nexusHeaderWriter(t.tracer, input.NexusHeader))
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
	endSpan, err := startOutboundWorkflowSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation: "ContinueAsNew",
		Name:      info.WorkflowType.Name,
		Tags:      workflowTags(info),
		Time:      workflow.Now(ctx),
	}, t.root.workflowHeaderWriter(t.tracer, ctx))
	if err != nil {
		return err
	}
	defer endSpan(nil)

	return t.Next.NewContinueAsNewError(ctx, wfn, args...)
}

type tracingNexusOperationInboundInterceptor struct {
	interceptor.NexusOperationInboundInterceptorBase
	root   *tracingInterceptor
	tracer Tracer
}

func (t *tracingNexusOperationInboundInterceptor) CancelOperation(ctx context.Context, input interceptor.NexusCancelOperationInput) (err error) {
	info := nexus.ExtractHandlerInfo(ctx)
	ctx, endSpan, err := startInboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation:  "RunCancelNexusOperationHandler",
		Name:       info.Service + "/" + info.Operation,
		DependedOn: true,
	}, t.root.nexusHeaderReader(t.tracer, input.Options.Header))
	if err != nil {
		return err
	}
	defer endSpan(&err)

	return t.Next.CancelOperation(ctx, input)
}

func (t *tracingNexusOperationInboundInterceptor) StartOperation(ctx context.Context, input interceptor.NexusStartOperationInput) (ret nexus.HandlerStartOperationResult[any], err error) {
	info := nexus.ExtractHandlerInfo(ctx)
	ctx, endSpan, err := startInboundSpan(t.tracer, ctx, &TracerStartSpanOptions{
		Operation:  "RunStartNexusOperationHandler",
		Name:       info.Service + "/" + info.Operation,
		DependedOn: true,
	}, t.root.nexusHeaderReader(t.tracer, input.Options.Header))
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

func (t *tracingInterceptor) headerWriter(tracer Tracer, ctx context.Context) func(TracerSpan) error {
	header := interceptor.Header(ctx)
	return func(span TracerSpan) error {
		return t.writeSpanToHeader(tracer, span, header)
	}
}

func (t *tracingInterceptor) workflowHeaderReader(tracer WorkflowTracer, ctx workflow.Context) func() (TracerSpanRef, error) {
	header := interceptor.WorkflowHeader(ctx)
	return func() (TracerSpanRef, error) {
		return t.readSpanFromHeader(tracer, header)
	}
}

func (t *tracingInterceptor) workflowHeaderWriter(tracer WorkflowTracer, ctx workflow.Context) func(TracerSpan) error {
	header := interceptor.WorkflowHeader(ctx)
	return func(span TracerSpan) error {
		return t.writeSpanToHeader(tracer, span, header)
	}
}

func (t *tracingInterceptor) nexusHeaderReader(tracer tracerCommon, header nexus.Header) func() (TracerSpanRef, error) {
	return func() (TracerSpanRef, error) {
		return t.readSpanFromNexusHeader(tracer, header)
	}
}

func (t *tracingInterceptor) nexusHeaderWriter(tracer tracerCommon, header nexus.Header) func(TracerSpan) error {
	return func(span TracerSpan) error {
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

func (t *tracingInterceptor) writeSpanToHeader(tracer tracerCommon, span TracerSpan, header map[string]*commonpb.Payload) error {
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

func (t *tracingInterceptor) writeSpanToNexusHeader(tracer tracerCommon, span TracerSpan, header nexus.Header) error {
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

func startInboundSpan(
	t Tracer,
	ctx context.Context,
	options *TracerStartSpanOptions,
	headerReader func() (TracerSpanRef, error),
) (context.Context, func(err *error), error) {
	options.Direction = SpanDirectionInbound

	parent, err := parentFromHeader(t, headerReader)
	if err != nil {
		return ctx, nil, err
	}
	options.Parent = parent

	span := t.CreateSpan(ctx, options)
	return t.ContextWithSpan(ctx, span), finishSpan(span), nil
}

func startInboundWorkflowSpan(
	t WorkflowTracer,
	ctx workflow.Context,
	options *TracerStartSpanOptions,
	headerReader func() (TracerSpanRef, error),
) (workflow.Context, func(err *error), error) {
	options.Direction = SpanDirectionInbound

	parent, err := parentFromHeader(t, headerReader)
	if err != nil {
		return ctx, nil, err
	}
	options.Parent = parent

	span := t.CreateSpan(ctx, options)
	return t.ContextWithSpan(ctx, span), finishSpan(span), nil
}

func startOutboundSpan(
	t Tracer,
	ctx context.Context,
	options *TracerStartSpanOptions,
	headerWriter func(TracerSpan) error,
) (func(err *error), error) {
	options.Direction = SpanDirectionOutbound
	options.Parent = t.SpanFromContext(ctx)

	span := t.CreateSpan(ctx, options)
	if err := headerWriter(span); err != nil {
		return nil, err
	}

	return finishSpan(span), nil
}

func startOutboundWorkflowSpan(
	t WorkflowTracer,
	ctx workflow.Context,
	options *TracerStartSpanOptions,
	headerWriter func(TracerSpan) error,
) (func(err *error), error) {
	options.Direction = SpanDirectionOutbound
	options.Parent = t.SpanFromContext(ctx)

	span := t.CreateSpan(ctx, options)
	if err := headerWriter(span); err != nil {
		return nil, err
	}

	return finishSpan(span), nil
}

func parentFromHeader(t tracerCommon, read func() (TracerSpanRef, error)) (TracerSpanRef, error) {
	span, err := read()
	if err != nil && !t.Options().AllowInvalidParentSpans {
		return nil, err
	}
	return span, nil
}

func finishSpan(span TracerSpan) func(err *error) {
	return func(err *error) {
		opts := &TracerFinishSpanOptions{}
		if err != nil {
			opts.Error = *err
		}
		span.Finish(opts)
	}
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
