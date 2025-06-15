package interceptor

import (
	"context"
	"fmt"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

const (
	workflowIDTagKey = "temporalWorkflowID"
	runIDTagKey      = "temporalRunID"
	activityIDTagKey = "temporalActivityID"
	updateIDTagKey   = "temporalUpdateID"
)

// Tracer is an interface for tracing implementations as used by
// NewTracingInterceptor. Most callers do not use this directly, but rather use
// the opentracing or opentelemetry packages.
//
// All implementations must embed BaseTracer to safely
// handle future changes.
type Tracer interface {
	// Options returns the options for the tracer. This is only called once on
	// initialization.
	Options() TracerOptions

	// UnmarshalSpan unmarshals the given map into a span reference.
	UnmarshalSpan(map[string]string) (TracerSpanRef, error)

	// MarshalSpan marshals the given span into a map. If the map is empty with no
	// error, the span is simply not set.
	MarshalSpan(TracerSpan) (map[string]string, error)

	// SpanFromContext returns the span from the general Go context or nil if not
	// present.
	SpanFromContext(context.Context) TracerSpan

	// ContextWithSpan creates a general Go context with the given span set.
	ContextWithSpan(context.Context, TracerSpan) context.Context

	// StartSpan starts and returns a span with the given options.
	StartSpan(*TracerStartSpanOptions) (TracerSpan, error)

	// GetLogger returns a log.Logger which may include additional fields in its
	// output in order to support correlation of tracing and log data.
	GetLogger(log.Logger, TracerSpanRef) log.Logger
	// SpanName can be used to give a custom name to a Span according to the input TracerStartSpanOptions,
	// or the decision can be deferred to the BaseTracer implementation.
	SpanName(options *TracerStartSpanOptions) string

	mustEmbedBaseTracer()
}

// BaseTracer is a default implementation of Tracer meant for embedding.
type BaseTracer struct{}

func (BaseTracer) GetLogger(logger log.Logger, ref TracerSpanRef) log.Logger {
	return logger
}
func (BaseTracer) SpanName(options *TracerStartSpanOptions) string {
	return fmt.Sprintf("%s:%s", options.Operation, options.Name)
}

//lint:ignore U1000 Ignore unused method; it is only required to implement the Tracer interface but will never be called.
func (BaseTracer) mustEmbedBaseTracer() {}

// TracerOptions are options returned from Tracer.Options.
type TracerOptions struct {
	// SpanContextKey provides a key to put a span on a context unrelated to how a
	// span might otherwise be put on a context by ContextWithSpan. This should
	// never be nil.
	//
	// This is used internally to set the span on contexts not natively supported
	// by tracing systems such as [workflow.Context].
	SpanContextKey interface{}

	// HeaderKey is the key name on the Temporal header to serialize the span to.
	// This should never be empty.
	HeaderKey string

	// DisableSignalTracing can be set to disable signal tracing.
	DisableSignalTracing bool

	// DisableQueryTracing can be set to disable query tracing.
	DisableQueryTracing bool

	// DisableUpdateTracing can be set to disable update tracing.
	DisableUpdateTracing bool

	// EnableSpanLinks can be set to enable span links for spans disconnected from their parent span.
	EnableSpanLinks bool

	// DisconnectContinueAsNew can be set to disconnect ContinueAsNew workflows from their parent span.
	DisconnectContinueAsNewWorkflows bool

	// AllowInvalidParentSpans will swallow errors interpreting parent
	// spans from headers. Useful when migrating from one tracing library
	// to another, while workflows/activities may be in progress.
	AllowInvalidParentSpans bool
}

// TracerStartSpanOptions are options for Tracer.StartSpan.
type TracerStartSpanOptions struct {
	// Parent is the optional parent reference of the span.
	Parent TracerSpanRef
	// Operation is the general operation name without the specific name.
	Operation string

	// Name is the specific activity, workflow, etc for the operation.
	Name string

	// Time indicates the start time of the span.
	//
	// For RunWorkflow and RunActivity operation types, this will match workflow.Info.WorkflowStartTime and
	// activity.Info.StartedTime respectively. All other operations use time.Now().
	Time time.Time

	// DependedOn is true if the parent depends on this span or false if it just
	// is related to the parent. In OpenTracing terms, this is true for "ChildOf"
	// reference types and false for "FollowsFrom" reference types.
	DependedOn bool

	// Tags are a set of span tags.
	Tags map[string]string

	// FromHeader is used internally, not by tracer implementations, to determine
	// whether the parent span can be retrieved from the Temporal header.
	FromHeader bool

	// ToHeader is used internally, not by tracer implementations, to determine
	// whether the span should be placed on the Temporal header.
	ToHeader bool

	// IdempotencyKey may optionally be used by tracing implementations to generate
	// deterministic span IDs.
	//
	// This is useful in workflow contexts where spans may need to be "resumed" before
	// ultimately being reported. Generating a deterministic span ID ensures that any
	// child spans created before the parent span is resumed do not become orphaned.
	//
	// IdempotencyKey is not guaranteed to be set for all operations; Tracer
	// implementations MUST therefore ignore zero values for this field.
	//
	// IdempotencyKey should be treated as opaque data by Tracer implementations.
	// Do not attempt to parse it, as the format is subject to change.
	IdempotencyKey string

	// Link is a link to a parent span.
	Link TracerSpanRef
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
	InterceptorBase
	tracer  Tracer
	options TracerOptions
}

// NewTracingInterceptor creates a new interceptor using the given tracer. Most
// callers do not use this directly, but rather use the opentracing or
// opentelemetry packages. This panics if options are not set as expected.
func NewTracingInterceptor(tracer Tracer) Interceptor {
	options := tracer.Options()
	if options.SpanContextKey == nil {
		panic("missing span context key")
	} else if options.HeaderKey == "" {
		panic("missing header key")
	}
	return &tracingInterceptor{tracer: tracer, options: options}
}

func (t *tracingInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	i := &tracingClientOutboundInterceptor{root: t}
	i.Next = next
	return i
}

func (t *tracingInterceptor) InterceptActivity(
	ctx context.Context,
	next ActivityInboundInterceptor,
) ActivityInboundInterceptor {
	i := &tracingActivityInboundInterceptor{root: t}
	i.Next = next
	return i
}

func (t *tracingInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next WorkflowInboundInterceptor,
) WorkflowInboundInterceptor {
	i := &tracingWorkflowInboundInterceptor{root: t, info: workflow.GetInfo(ctx)}
	i.Next = next
	return i
}

func (t *tracingInterceptor) InterceptNexusOperation(
	ctx context.Context,
	next NexusOperationInboundInterceptor,
) NexusOperationInboundInterceptor {
	i := &tracingNexusOperationInboundInterceptor{root: t}
	i.Next = next
	return i
}

type tracingClientOutboundInterceptor struct {
	ClientOutboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingClientOutboundInterceptor) CreateSchedule(ctx context.Context, in *ScheduleClientCreateInput) (client.ScheduleHandle, error) {
	// Start span and write to header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation: "CreateSchedule",
		Name:      in.Options.ID,
		ToHeader:  true,
		Time:      time.Now(),
	}, t.root.headerReader(ctx), t.root.headerWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	run, err := t.Next.CreateSchedule(ctx, in)
	finishOpts.Error = err
	return run, err
}

func (t *tracingClientOutboundInterceptor) ExecuteWorkflow(
	ctx context.Context,
	in *ClientExecuteWorkflowInput,
) (client.WorkflowRun, error) {
	// Start span and write to header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation: "StartWorkflow",
		Name:      in.WorkflowType,
		Tags:      map[string]string{workflowIDTagKey: in.Options.ID},
		ToHeader:  true,
		Time:      time.Now(),
	}, t.root.headerReader(ctx), t.root.headerWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	run, err := t.Next.ExecuteWorkflow(ctx, in)
	finishOpts.Error = err
	return run, err
}

func (t *tracingClientOutboundInterceptor) SignalWorkflow(ctx context.Context, in *ClientSignalWorkflowInput) error {
	// Only add tracing if enabled
	if t.root.options.DisableSignalTracing {
		return t.Next.SignalWorkflow(ctx, in)
	}
	// Start span and write to header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation: "SignalWorkflow",
		Name:      in.SignalName,
		Tags:      map[string]string{workflowIDTagKey: in.WorkflowID},
		ToHeader:  true,
		Time:      time.Now(),
	}, t.root.headerReader(ctx), t.root.headerWriter(ctx))
	if err != nil {
		return err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	err = t.Next.SignalWorkflow(ctx, in)
	finishOpts.Error = err
	return err
}

func (t *tracingClientOutboundInterceptor) SignalWithStartWorkflow(
	ctx context.Context,
	in *ClientSignalWithStartWorkflowInput,
) (client.WorkflowRun, error) {
	// Start span and write to header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation: "SignalWithStartWorkflow",
		Name:      in.WorkflowType,
		Tags:      map[string]string{workflowIDTagKey: in.Options.ID},
		ToHeader:  true,
	}, t.root.headerReader(ctx), t.root.headerWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	run, err := t.Next.SignalWithStartWorkflow(ctx, in)
	finishOpts.Error = err
	return run, err
}

func (t *tracingClientOutboundInterceptor) QueryWorkflow(
	ctx context.Context,
	in *ClientQueryWorkflowInput,
) (converter.EncodedValue, error) {
	// Only add tracing if enabled
	if t.root.options.DisableQueryTracing {
		return t.Next.QueryWorkflow(ctx, in)
	}
	// Start span and write to header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation: "QueryWorkflow",
		Name:      in.QueryType,
		Tags:      map[string]string{workflowIDTagKey: in.WorkflowID},
		ToHeader:  true,
		Time:      time.Now(),
	}, t.root.headerReader(ctx), t.root.headerWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	val, err := t.Next.QueryWorkflow(ctx, in)
	finishOpts.Error = err
	return val, err
}

func (t *tracingClientOutboundInterceptor) UpdateWorkflow(
	ctx context.Context,
	in *ClientUpdateWorkflowInput,
) (client.WorkflowUpdateHandle, error) {
	// Only add tracing if enabled
	if t.root.options.DisableUpdateTracing {
		return t.Next.UpdateWorkflow(ctx, in)
	}
	// Start span and write to header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation: "UpdateWorkflow",
		Name:      in.UpdateName,
		Tags:      map[string]string{workflowIDTagKey: in.WorkflowID},
		ToHeader:  true,
		Time:      time.Now(),
	}, t.root.headerReader(ctx), t.root.headerWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	val, err := t.Next.UpdateWorkflow(ctx, in)
	finishOpts.Error = err
	return val, err
}

func (t *tracingClientOutboundInterceptor) UpdateWithStartWorkflow(
	ctx context.Context,
	in *ClientUpdateWithStartWorkflowInput,
) (client.WorkflowUpdateHandle, error) {
	// Only add tracing if enabled
	if t.root.options.DisableUpdateTracing {
		return t.Next.UpdateWithStartWorkflow(ctx, in)
	}
	// Start span and write to header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation: "UpdateWithStartWorkflow",
		Name:      in.UpdateOptions.UpdateName,
		Tags:      map[string]string{workflowIDTagKey: in.UpdateOptions.WorkflowID, updateIDTagKey: in.UpdateOptions.UpdateID},
		ToHeader:  true,
		Time:      time.Now(),
	}, t.root.headerReader(ctx), t.root.headerWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	val, err := t.Next.UpdateWithStartWorkflow(ctx, in)
	finishOpts.Error = err
	return val, err
}

type tracingActivityOutboundInterceptor struct {
	ActivityOutboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingActivityOutboundInterceptor) GetLogger(ctx context.Context) log.Logger {
	if span := t.root.tracer.SpanFromContext(ctx); span != nil {
		return t.root.tracer.GetLogger(t.Next.GetLogger(ctx), span)
	}
	return t.Next.GetLogger(ctx)
}

type tracingActivityInboundInterceptor struct {
	ActivityInboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingActivityInboundInterceptor) Init(outbound ActivityOutboundInterceptor) error {
	i := &tracingActivityOutboundInterceptor{root: t.root}
	i.Next = outbound
	return t.Next.Init(i)
}

func (t *tracingActivityInboundInterceptor) ExecuteActivity(
	ctx context.Context,
	in *ExecuteActivityInput,
) (interface{}, error) {
	// Start span reading from header
	info := activity.GetInfo(ctx)
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation:  "RunActivity",
		Name:       info.ActivityType.Name,
		DependedOn: true,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
			activityIDTagKey: info.ActivityID,
		},
		FromHeader: true,
		Time:       info.StartedTime,
	}, t.root.headerReader(ctx), t.root.headerWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	ret, err := t.Next.ExecuteActivity(ctx, in)
	finishOpts.Error = err
	return ret, err
}

type tracingWorkflowInboundInterceptor struct {
	WorkflowInboundInterceptorBase
	root        *tracingInterceptor
	spanCounter uint16
	info        *workflow.Info
}

// newIdempotencyKey returns a new idempotency key by incrementing the span counter and interpolating
// this new value into a string that includes the workflow namespace/id/run id and the interceptor type.
func (t *tracingWorkflowInboundInterceptor) newIdempotencyKey() string {
	t.spanCounter++
	return fmt.Sprintf("WorkflowInboundInterceptor:%s:%s:%s:%d",
		t.info.Namespace,
		t.info.WorkflowExecution.ID,
		t.info.WorkflowExecution.RunID,
		t.spanCounter)
}

func (t *tracingWorkflowInboundInterceptor) Init(outbound WorkflowOutboundInterceptor) error {
	i := &tracingWorkflowOutboundInterceptor{root: t.root}
	i.Next = outbound
	return t.Next.Init(i)
}

func (t *tracingWorkflowInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *ExecuteWorkflowInput,
) (interface{}, error) {
	// Start span reading from header
	span, ctx, err := t.root.startSpanFromWorkflowContext(ctx, &TracerStartSpanOptions{
		Operation: "RunWorkflow",
		Name:      t.info.WorkflowType.Name,
		Tags: map[string]string{
			workflowIDTagKey: t.info.WorkflowExecution.ID,
			runIDTagKey:      t.info.WorkflowExecution.RunID,
		},
		FromHeader:     true,
		Time:           t.info.WorkflowStartTime,
		IdempotencyKey: t.newIdempotencyKey(),
	}, t.root.workflowHeaderReader(ctx), t.root.workflowHeaderWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	ret, err := t.Next.ExecuteWorkflow(ctx, in)
	finishOpts.Error = err
	return ret, err
}

func (t *tracingWorkflowInboundInterceptor) HandleSignal(ctx workflow.Context, in *HandleSignalInput) error {
	// Only add tracing if enabled and not replaying
	if t.root.options.DisableSignalTracing || workflow.IsReplaying(ctx) {
		return t.Next.HandleSignal(ctx, in)
	}
	// Start span reading from header
	info := workflow.GetInfo(ctx)
	span, ctx, err := t.root.startSpanFromWorkflowContext(ctx, &TracerStartSpanOptions{
		Operation: "HandleSignal",
		Name:      in.SignalName,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		FromHeader:     true,
		Time:           time.Now(),
		IdempotencyKey: t.newIdempotencyKey(),
	}, t.root.workflowHeaderReader(ctx), t.root.workflowHeaderWriter(ctx))
	if err != nil {
		return err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	err = t.Next.HandleSignal(ctx, in)
	finishOpts.Error = err
	return err
}

func (t *tracingWorkflowInboundInterceptor) HandleQuery(
	ctx workflow.Context,
	in *HandleQueryInput,
) (interface{}, error) {
	// Only add tracing if enabled and not replaying
	if t.root.options.DisableQueryTracing || workflow.IsReplaying(ctx) {
		return t.Next.HandleQuery(ctx, in)
	}
	// Start span reading from header
	info := workflow.GetInfo(ctx)
	span, ctx, err := t.root.startSpanFromWorkflowContext(ctx, &TracerStartSpanOptions{
		Operation: "HandleQuery",
		Name:      in.QueryType,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		FromHeader: true,
		Time:       time.Now(),
		// We intentionally do not set IdempotencyKey here because queries are not recorded in
		// workflow history. When the tracing interceptor's span counter is reset between workflow
		// replays, old queries will not be processed which could result in idempotency key
		// collisions with other queries or signals.
	}, t.root.workflowHeaderReader(ctx), t.root.workflowHeaderWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	val, err := t.Next.HandleQuery(ctx, in)
	finishOpts.Error = err
	return val, err
}

func (t *tracingWorkflowInboundInterceptor) ValidateUpdate(
	ctx workflow.Context,
	in *UpdateInput,
) error {
	// Only add tracing if enabled and not replaying
	if t.root.options.DisableUpdateTracing {
		return t.Next.ValidateUpdate(ctx, in)
	}
	// Start span reading from header
	info := workflow.GetInfo(ctx)
	currentUpdateInfo := workflow.GetCurrentUpdateInfo(ctx)
	span, ctx, err := t.root.startSpanFromWorkflowContext(ctx, &TracerStartSpanOptions{
		Operation: "ValidateUpdate",
		Name:      in.Name,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
			updateIDTagKey:   currentUpdateInfo.ID,
		},
		FromHeader: true,
		Time:       time.Now(),
		// We intentionally do not set IdempotencyKey here because validation is not run on
		// replay. When the tracing interceptor's span counter is reset between workflow
		// replays, the validator will not be processed which could result in impotency key
		// collisions with other requests.
	}, t.root.workflowHeaderReader(ctx), t.root.workflowHeaderWriter(ctx))
	if err != nil {
		return err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	err = t.Next.ValidateUpdate(ctx, in)
	finishOpts.Error = err
	return err
}

func (t *tracingWorkflowInboundInterceptor) ExecuteUpdate(
	ctx workflow.Context,
	in *UpdateInput,
) (interface{}, error) {
	// Only add tracing if enabled and not replaying
	if t.root.options.DisableUpdateTracing {
		return t.Next.ExecuteUpdate(ctx, in)
	}
	// Start span reading from header
	info := workflow.GetInfo(ctx)
	currentUpdateInfo := workflow.GetCurrentUpdateInfo(ctx)
	span, ctx, err := t.root.startSpanFromWorkflowContext(ctx, &TracerStartSpanOptions{
		// Using operation name "HandleUpdate" to match other SDKs and by consistence with other operations
		Operation: "HandleUpdate",
		Name:      in.Name,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
			updateIDTagKey:   currentUpdateInfo.ID,
		},
		FromHeader:     true,
		Time:           time.Now(),
		IdempotencyKey: t.newIdempotencyKey(),
	}, t.root.workflowHeaderReader(ctx), t.root.workflowHeaderWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	val, err := t.Next.ExecuteUpdate(ctx, in)
	finishOpts.Error = err
	return val, err
}

type tracingWorkflowOutboundInterceptor struct {
	WorkflowOutboundInterceptorBase
	root *tracingInterceptor
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	// Start span writing to header
	span, ctx, err := t.startNonReplaySpan(ctx, "StartActivity", activityType, true, t.root.workflowHeaderWriter(ctx))
	if err != nil {
		return err
	}
	defer span.Finish(&TracerFinishSpanOptions{})

	return t.Next.ExecuteActivity(ctx, activityType, args...)
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteLocalActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	// Start span writing to header
	span, ctx, err := t.startNonReplaySpan(ctx, "StartActivity", activityType, true, t.root.workflowHeaderWriter(ctx))
	if err != nil {
		return err
	}
	defer span.Finish(&TracerFinishSpanOptions{})

	return t.Next.ExecuteLocalActivity(ctx, activityType, args...)
}

func (t *tracingWorkflowOutboundInterceptor) GetLogger(ctx workflow.Context) log.Logger {
	if span, _ := ctx.Value(t.root.options.SpanContextKey).(TracerSpan); span != nil {
		return t.root.tracer.GetLogger(t.Next.GetLogger(ctx), span)
	}
	return t.Next.GetLogger(ctx)
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteChildWorkflow(
	ctx workflow.Context,
	childWorkflowType string,
	args ...interface{},
) workflow.ChildWorkflowFuture {
	// Start span writing to header
	span, ctx, errFut := t.startNonReplaySpan(ctx, "StartChildWorkflow", childWorkflowType, false, t.root.workflowHeaderWriter(ctx))
	if errFut != nil {
		return childWorkflowFuture{errFut}
	}
	defer span.Finish(&TracerFinishSpanOptions{})

	return t.Next.ExecuteChildWorkflow(ctx, childWorkflowType, args...)
}

func (t *tracingWorkflowOutboundInterceptor) SignalExternalWorkflow(
	ctx workflow.Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) workflow.Future {
	// Start span writing to header if enabled
	if !t.root.options.DisableSignalTracing {
		var span TracerSpan
		var futErr workflow.Future
		span, ctx, futErr = t.startNonReplaySpan(ctx, "SignalExternalWorkflow", signalName, false, t.root.workflowHeaderWriter(ctx))
		if futErr != nil {
			return futErr
		}
		defer span.Finish(&TracerFinishSpanOptions{})
	}

	return t.Next.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (t *tracingWorkflowOutboundInterceptor) SignalChildWorkflow(
	ctx workflow.Context,
	workflowID string,
	signalName string,
	arg interface{},
) workflow.Future {
	// Start span writing to header if enabled
	if !t.root.options.DisableSignalTracing {
		var span TracerSpan
		var futErr workflow.Future
		span, ctx, futErr = t.startNonReplaySpan(ctx, "SignalChildWorkflow", signalName, false, t.root.workflowHeaderWriter(ctx))
		if futErr != nil {
			return futErr
		}
		defer span.Finish(&TracerFinishSpanOptions{})
	}

	return t.Next.SignalChildWorkflow(ctx, workflowID, signalName, arg)
}

func (t *tracingWorkflowOutboundInterceptor) ExecuteNexusOperation(ctx workflow.Context, input ExecuteNexusOperationInput) workflow.NexusOperationFuture {
	// Start span writing to header
	var ok bool
	var operationName string
	if operationName, ok = input.Operation.(string); ok {
	} else if regOp, ok := input.Operation.(interface{ Name() string }); ok {
		operationName = regOp.Name()
	} else {
		return nexusOperationFuture{workflowFutureFromErr(ctx, fmt.Errorf("unexpected operation type: %v", input.Operation))}
	}
	span, ctx, futErr := t.startNonReplaySpan(ctx, "StartNexusOperation", input.Client.Service()+"/"+operationName, false, t.root.nexusHeaderWriter(input.NexusHeader))
	if futErr != nil {
		return nexusOperationFuture{futErr}
	}
	defer span.Finish(&TracerFinishSpanOptions{})

	return t.Next.ExecuteNexusOperation(ctx, input)
}

func (t *tracingWorkflowOutboundInterceptor) NewContinueAsNewError(
	ctx workflow.Context,
	wfn interface{},
	args ...interface{},
) error {
	err := t.Next.NewContinueAsNewError(ctx, wfn, args...)

	if !workflow.IsReplaying(ctx) && workflow.IsContinueAsNewError(err) {
		// This will either be the parent span or a new root span
		span, _ := ctx.Value(t.root.options.SpanContextKey).(TracerSpan)

		if t.root.options.DisconnectContinueAsNewWorkflows {
			info := workflow.GetInfo(ctx)

			// Start a new root span detached from the parent
			opts := &TracerStartSpanOptions{
				Operation:  "ContinueAsNew",
				Name:       info.WorkflowType.Name,
				DependedOn: false,
				Tags: map[string]string{
					workflowIDTagKey: info.WorkflowExecution.ID,
					runIDTagKey:      info.WorkflowExecution.RunID,
				},
				ToHeader: true,
				Time:     time.Now(),
				Link:     nil,
			}

			// Connect the new root span to the parent with a link
			if t.root.options.EnableSpanLinks {
				if parentSpan, ok := ctx.Value(t.root.options.SpanContextKey).(TracerSpan); ok {
					opts.Link = parentSpan
				}
			}

			s, err := t.root.tracer.StartSpan(opts)
			if err != nil {
				return fmt.Errorf("failed to start detached span for ContinueAsNew: %w", err)
			}
			span = s

			var finishOpts TracerFinishSpanOptions
			defer span.Finish(&finishOpts)
		}

		header := WorkflowHeader(ctx)

		if span != nil {
			if writeErr := t.root.writeSpanToHeader(span, header); writeErr != nil {
				return fmt.Errorf("failed writing span when creating continue as new error: %w", writeErr)
			}
		}
	}

	return err
}

type nopSpan struct{}

func (nopSpan) Finish(*TracerFinishSpanOptions) {}

// Span always returned, even in replay. futErr is non-nil on error.
func (t *tracingWorkflowOutboundInterceptor) startNonReplaySpan(
	ctx workflow.Context,
	operation string,
	name string,
	dependedOn bool,
	headerWriter func(TracerSpan) error,
) (span TracerSpan, newCtx workflow.Context, futErr workflow.Future) {
	// Noop span if replaying
	if workflow.IsReplaying(ctx) {
		return nopSpan{}, ctx, nil
	}
	info := workflow.GetInfo(ctx)
	span, newCtx, err := t.root.startSpanFromWorkflowContext(ctx, &TracerStartSpanOptions{
		Operation:  operation,
		Name:       name,
		DependedOn: dependedOn,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		ToHeader: true,
		Time:     time.Now(),
	}, t.root.workflowHeaderReader(ctx), headerWriter)
	if err != nil {
		return nopSpan{}, ctx, workflowFutureFromErr(ctx, err)
	}
	return span, newCtx, nil
}

type tracingNexusOperationInboundInterceptor struct {
	NexusOperationInboundInterceptorBase
	root *tracingInterceptor
}

// CancelOperation implements internal.NexusOperationInboundInterceptor.
func (t *tracingNexusOperationInboundInterceptor) CancelOperation(ctx context.Context, input NexusCancelOperationInput) error {
	info := nexus.ExtractHandlerInfo(ctx)
	// Start span reading from header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation:  "RunCancelNexusOperationHandler",
		Name:       info.Service + "/" + info.Operation,
		DependedOn: true,
		FromHeader: true,
		Time:       time.Now(),
	}, t.root.nexusHeaderReader(input.Options.Header), t.root.headerWriter(ctx))
	if err != nil {
		return err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	err = t.Next.CancelOperation(ctx, input)
	finishOpts.Error = err
	return err
}

// StartOperation implements internal.NexusOperationInboundInterceptor.
func (t *tracingNexusOperationInboundInterceptor) StartOperation(ctx context.Context, input NexusStartOperationInput) (nexus.HandlerStartOperationResult[any], error) {
	info := nexus.ExtractHandlerInfo(ctx)
	// Start span reading from header
	span, ctx, err := t.root.startSpanFromContext(ctx, &TracerStartSpanOptions{
		Operation:  "RunStartNexusOperationHandler",
		Name:       info.Service + "/" + info.Operation,
		DependedOn: true,
		FromHeader: true,
		Time:       time.Now(),
	}, t.root.nexusHeaderReader(input.Options.Header), t.root.headerWriter(ctx))
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	ret, err := t.Next.StartOperation(ctx, input)
	finishOpts.Error = err
	return ret, err
}

func (t *tracingInterceptor) startSpanFromContext(
	ctx context.Context,
	options *TracerStartSpanOptions,
	headerReader func() (TracerSpanRef, error),
	headerWriter func(span TracerSpan) error,
) (TracerSpan, context.Context, error) {
	// Try to get parent from context
	options.Parent = t.tracer.SpanFromContext(ctx)
	span, err := t.startSpan(ctx, options, headerReader, headerWriter)
	if err != nil {
		return nil, nil, err
	}
	return span, t.tracer.ContextWithSpan(context.WithValue(ctx, t.options.SpanContextKey, span), span), nil
}

func (t *tracingInterceptor) startSpanFromWorkflowContext(
	ctx workflow.Context,
	options *TracerStartSpanOptions,
	headerReader func() (TracerSpanRef, error),
	headerWriter func(span TracerSpan) error,
) (TracerSpan, workflow.Context, error) {
	span, err := t.startSpan(ctx, options, headerReader, headerWriter)
	if err != nil {
		return nil, nil, err
	}
	return span, workflow.WithValue(ctx, t.options.SpanContextKey, span), nil
}

// Note, this does not put the span on the context
func (t *tracingInterceptor) startSpan(
	ctx interface{ Value(interface{}) interface{} },
	options *TracerStartSpanOptions,
	headerReader func() (TracerSpanRef, error),
	headerWriter func(span TracerSpan) error,
) (TracerSpan, error) {
	// Get parent span from header if not already present and allowed
	if options.Parent == nil && options.FromHeader {
		if span, err := headerReader(); err != nil && !t.options.AllowInvalidParentSpans {
			return nil, err
		} else if span != nil {
			options.Parent = span
		}
	}

	// If no parent span, try to get from context
	if options.Parent == nil {
		options.Parent, _ = ctx.Value(t.options.SpanContextKey).(TracerSpan)
	}

	// Start the span
	span, err := t.tracer.StartSpan(options)
	if err != nil {
		return nil, err
	}

	// Put span in header if wanted
	if options.ToHeader {
		if err := headerWriter(span); err != nil {
			return nil, err
		}
	}
	return span, nil
}

func (t *tracingInterceptor) headerReader(ctx context.Context) func() (TracerSpanRef, error) {
	header := Header(ctx)
	return func() (TracerSpanRef, error) {
		return t.readSpanFromHeader(header)
	}
}

func (t *tracingInterceptor) headerWriter(ctx context.Context) func(TracerSpan) error {
	header := Header(ctx)
	return func(span TracerSpan) error {
		return t.writeSpanToHeader(span, header)
	}
}

func (t *tracingInterceptor) workflowHeaderReader(ctx workflow.Context) func() (TracerSpanRef, error) {
	header := WorkflowHeader(ctx)
	return func() (TracerSpanRef, error) {
		return t.readSpanFromHeader(header)
	}
}

func (t *tracingInterceptor) workflowHeaderWriter(ctx workflow.Context) func(TracerSpan) error {
	header := WorkflowHeader(ctx)
	return func(span TracerSpan) error {
		return t.writeSpanToHeader(span, header)
	}
}

func (t *tracingInterceptor) nexusHeaderReader(header nexus.Header) func() (TracerSpanRef, error) {
	return func() (TracerSpanRef, error) {
		return t.readSpanFromNexusHeader(header)
	}
}

func (t *tracingInterceptor) nexusHeaderWriter(header nexus.Header) func(TracerSpan) error {
	return func(span TracerSpan) error {
		return t.writeSpanToNexusHeader(span, header)
	}
}

func (t *tracingInterceptor) readSpanFromHeader(header map[string]*commonpb.Payload) (TracerSpanRef, error) {
	// Get from map
	payload := header[t.options.HeaderKey]
	if payload == nil {
		return nil, nil
	}
	// Convert from the payload
	var data map[string]string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &data); err != nil {
		return nil, err
	}
	// Unmarshal
	return t.tracer.UnmarshalSpan(data)
}

func (t *tracingInterceptor) writeSpanToHeader(span TracerSpan, header map[string]*commonpb.Payload) error {
	// Serialize span to map
	data, err := t.tracer.MarshalSpan(span)
	if err != nil || len(data) == 0 {
		return err
	}
	// Convert to payload
	payload, err := converter.GetDefaultDataConverter().ToPayload(data)
	if err != nil {
		return err
	}
	// Put on header
	header[t.options.HeaderKey] = payload
	return nil
}

func (t *tracingInterceptor) writeSpanToNexusHeader(span TracerSpan, header nexus.Header) error {
	// Serialize span to map
	data, err := t.tracer.MarshalSpan(span)
	if err != nil || len(data) == 0 {
		return err
	}
	// Put on header
	for k, v := range data {
		header.Set(k, v)
	}
	return nil
}

func (t *tracingInterceptor) readSpanFromNexusHeader(header nexus.Header) (TracerSpanRef, error) {
	return t.tracer.UnmarshalSpan(header)
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
