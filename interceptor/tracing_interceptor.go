// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
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

package interceptor

import (
	"context"
	"fmt"
	"time"

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

	mustEmbedBaseTracer()
}

// BaseTracer is a default implementation of Tracer meant for embedding.
type BaseTracer struct{}

func (BaseTracer) GetLogger(logger log.Logger, ref TracerSpanRef) log.Logger {
	return logger
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
	// by tracing systems such as workflow.Context.
	SpanContextKey interface{}

	// HeaderKey is the key name on the Temporal header to serialize the span to.
	// This should never be empty.
	HeaderKey string

	// DisableSignalTracing can be set to disable signal tracing.
	DisableSignalTracing bool

	// DisableQueryTracing can be set to disable query tracing.
	DisableQueryTracing bool
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
}

// TracerSpanRef represents a span reference such as a parent.
type TracerSpanRef interface{}

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
	i := &tracingWorkflowInboundInterceptor{root: t}
	i.Next = next
	return i
}

type tracingClientOutboundInterceptor struct {
	ClientOutboundInterceptorBase
	root *tracingInterceptor
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
	})
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
	})
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
	})
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
	})
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	val, err := t.Next.QueryWorkflow(ctx, in)
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
	})
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
	root *tracingInterceptor
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
	info := workflow.GetInfo(ctx)
	span, ctx, err := t.root.startSpanFromWorkflowContext(ctx, &TracerStartSpanOptions{
		Operation: "RunWorkflow",
		Name:      info.WorkflowType.Name,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		FromHeader: true,
		Time:       info.WorkflowStartTime,
	})
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
		FromHeader: true,
		Time:       time.Now(),
	})
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
	})
	if err != nil {
		return nil, err
	}
	var finishOpts TracerFinishSpanOptions
	defer span.Finish(&finishOpts)

	val, err := t.Next.HandleQuery(ctx, in)
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
	span, ctx, err := t.startNonReplaySpan(ctx, "StartActivity", activityType, true)
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
	span, ctx, err := t.startNonReplaySpan(ctx, "StartActivity", activityType, true)
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
	span, ctx, err := t.startNonReplaySpan(ctx, "StartChildWorkflow", childWorkflowType, false)
	if err != nil {
		return err
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
		var futErr workflow.ChildWorkflowFuture
		span, ctx, futErr = t.startNonReplaySpan(ctx, "SignalExternalWorkflow", signalName, false)
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
		var futErr workflow.ChildWorkflowFuture
		span, ctx, futErr = t.startNonReplaySpan(ctx, "SignalChildWorkflow", signalName, false)
		if futErr != nil {
			return futErr
		}
		defer span.Finish(&TracerFinishSpanOptions{})
	}

	return t.Next.SignalChildWorkflow(ctx, workflowID, signalName, arg)
}

func (t *tracingWorkflowOutboundInterceptor) NewContinueAsNewError(
	ctx workflow.Context,
	wfn interface{},
	args ...interface{},
) error {
	err := t.Next.NewContinueAsNewError(ctx, wfn, args...)
	if !workflow.IsReplaying(ctx) {
		if contErr, _ := err.(*workflow.ContinueAsNewError); contErr != nil {
			// Get the current span and write header
			if span, _ := ctx.Value(t.root.options.SpanContextKey).(TracerSpan); span != nil {
				if writeErr := t.root.writeSpanToHeader(span, WorkflowHeader(ctx)); writeErr != nil {
					return fmt.Errorf("failed writing span when creating continue as new error: %w", writeErr)
				}
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
) (span TracerSpan, newCtx workflow.Context, futErr workflow.ChildWorkflowFuture) {
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
	})
	if err != nil {
		return nopSpan{}, ctx, newErrFut(ctx, err)
	}
	return span, newCtx, nil
}

func (t *tracingInterceptor) startSpanFromContext(
	ctx context.Context,
	options *TracerStartSpanOptions,
) (TracerSpan, context.Context, error) {
	// Try to get parent from context
	options.Parent = t.tracer.SpanFromContext(ctx)
	span, err := t.startSpan(ctx, Header(ctx), options)
	if err != nil {
		return nil, nil, err
	}
	return span, t.tracer.ContextWithSpan(context.WithValue(ctx, t.options.SpanContextKey, span), span), nil
}

func (t *tracingInterceptor) startSpanFromWorkflowContext(
	ctx workflow.Context,
	options *TracerStartSpanOptions,
) (TracerSpan, workflow.Context, error) {
	span, err := t.startSpan(ctx, WorkflowHeader(ctx), options)
	if err != nil {
		return nil, nil, err
	}
	return span, workflow.WithValue(ctx, t.options.SpanContextKey, span), nil
}

// Note, this does not put the span on the context
func (t *tracingInterceptor) startSpan(
	ctx interface{ Value(interface{}) interface{} },
	header map[string]*commonpb.Payload,
	options *TracerStartSpanOptions,
) (TracerSpan, error) {

	// Get parent span from header if not already present and allowed
	if options.Parent == nil && options.FromHeader {
		if span, err := t.readSpanFromHeader(header); err != nil {
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
	if options.ToHeader && header != nil {
		if err := t.writeSpanToHeader(span, header); err != nil {
			return nil, err
		}
	}
	return span, nil
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

func newErrFut(ctx workflow.Context, err error) workflow.ChildWorkflowFuture {
	fut, set := workflow.NewFuture(ctx)
	set.SetError(err)
	return errFut{fut}
}

type errFut struct{ workflow.Future }

func (e errFut) GetChildWorkflowExecution() workflow.Future { return e }

func (e errFut) SignalChildWorkflow(ctx workflow.Context, signalName string, data interface{}) workflow.Future {
	return e
}
