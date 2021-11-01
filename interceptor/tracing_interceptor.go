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

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

const (
	workflowIDTagKey = "temporalWorkflowID"
	runIDTagKey      = "temporalRunID"
)

// Tracer is an interface for tracing implementations as used by
// NewTracingInterceptor. Most callers do not use this directly, but rather use
// the opentracing or opentelemetry packages.
type Tracer interface {
	// SpanContextKey provides a key to put a span on a context unrelated to how a
	// span might otherwise be put on a context by ContextWithSpan. This should
	// never be nil.
	SpanContextKey() interface{}

	// HeaderKey is the key name on the Temporal header to serialize the span to.
	HeaderKey() string

	// UnmarshalSpan unmarshals the given map into a span reference.
	UnmarshalSpan(map[string]string) (TracerSpanRef, error)

	// MarshalSpan marshals the given span into a map.
	MarshalSpan(TracerSpan) (map[string]string, error)

	// SpanFromContext returns the span from the general Go context or nil if not
	// present.
	SpanFromContext(context.Context) TracerSpan

	// ContextWithSpan creates a general Go context with the given span set.
	ContextWithSpan(context.Context, TracerSpan) context.Context

	// StartSpan starts and returns a span with the given options.
	StartSpan(*TracerStartSpanOptions) (TracerSpan, error)
}

// TracerStartSpanOptions are options for Tracer.StartSpan.
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
	tracer Tracer
}

// NewTracingInterceptor creates a new interceptor for the given tracer. Most
// callers do not use this directly, but rather use the opentracing or
// opentelemetry packages.
func NewTracingInterceptor(tracer Tracer) Interceptor {
	return &tracingInterceptor{tracer: tracer}
}

func (t *tracingInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	i := &tracingClientOutboundInterceptor{tracer: t.tracer}
	i.Next = next
	return i
}

func (t *tracingInterceptor) InterceptActivity(
	ctx context.Context,
	next ActivityInboundInterceptor,
) ActivityInboundInterceptor {
	i := &tracingActivityInboundInterceptor{tracer: t.tracer}
	i.Next = next
	return i
}

func (t *tracingInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next WorkflowInboundInterceptor,
) WorkflowInboundInterceptor {
	i := &tracingWorkflowInboundInterceptor{tracer: t.tracer}
	i.Next = next
	return i
}

type tracingClientOutboundInterceptor struct {
	ClientOutboundInterceptorBase
	tracer Tracer
}

func (t *tracingClientOutboundInterceptor) ExecuteWorkflow(
	ctx context.Context,
	in *ClientExecuteWorkflowInput,
) (client.WorkflowRun, error) {
	// Start span and write to header
	span, ctx, err := startSpanFromContext(ctx, t.tracer, &TracerStartSpanOptions{
		Operation: "StartWorkflow",
		Name:      in.WorkflowType,
		Tags:      map[string]string{workflowIDTagKey: in.Options.ID},
		ToHeader:  true,
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

func (t *tracingClientOutboundInterceptor) SignalWithStartWorkflow(
	ctx context.Context,
	in *ClientSignalWithStartWorkflowInput,
) (client.WorkflowRun, error) {
	// Start span and write to header
	span, ctx, err := startSpanFromContext(ctx, t.tracer, &TracerStartSpanOptions{
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

type tracingActivityInboundInterceptor struct {
	ActivityInboundInterceptorBase
	tracer Tracer
}

func (t *tracingActivityInboundInterceptor) ExecuteActivity(
	ctx context.Context,
	in *ExecuteActivityInput,
) (interface{}, error) {
	// Start span reading from header
	info := activity.GetInfo(ctx)
	span, ctx, err := startSpanFromContext(ctx, t.tracer, &TracerStartSpanOptions{
		Operation:  "RunActivity",
		Name:       info.ActivityType.Name,
		DependedOn: true,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		FromHeader: true,
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
	tracer Tracer
}

func (t *tracingWorkflowInboundInterceptor) Init(outbound WorkflowOutboundInterceptor) error {
	i := &tracingWorkflowOutboundInterceptor{tracer: t.tracer}
	i.Next = outbound
	return t.Next.Init(i)
}

func (t *tracingWorkflowInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *ExecuteWorkflowInput,
) (interface{}, error) {
	// Start span reading from header
	info := workflow.GetInfo(ctx)
	span, ctx, err := startSpanFromWorkflowContext(ctx, t.tracer, &TracerStartSpanOptions{
		Operation: "RunWorkflow",
		Name:      info.WorkflowType.Name,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		FromHeader: true,
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

type tracingWorkflowOutboundInterceptor struct {
	WorkflowOutboundInterceptorBase
	tracer Tracer
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

func (t *tracingWorkflowOutboundInterceptor) NewContinueAsNewError(
	ctx workflow.Context,
	wfn interface{},
	args ...interface{},
) error {
	err := t.Next.NewContinueAsNewError(ctx, wfn, args...)
	if !workflow.IsReplaying(ctx) {
		if contErr, _ := err.(*workflow.ContinueAsNewError); contErr != nil {
			// Get the current span and write header
			if span, _ := ctx.Value(t.tracer.SpanContextKey()).(TracerSpan); span != nil {
				if writeErr := writeSpanToHeader(t.tracer, span, WorkflowHeader(ctx)); writeErr != nil {
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
	span, newCtx, err := startSpanFromWorkflowContext(ctx, t.tracer, &TracerStartSpanOptions{
		Operation:  operation,
		Name:       name,
		DependedOn: dependedOn,
		Tags: map[string]string{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		ToHeader: true,
	})
	if err != nil {
		return nopSpan{}, ctx, newErrFut(ctx, err)
	}
	return span, newCtx, nil
}

func startSpanFromContext(
	ctx context.Context,
	tracer Tracer,
	options *TracerStartSpanOptions,
) (TracerSpan, context.Context, error) {
	// Try to get parent from context
	options.Parent = tracer.SpanFromContext(ctx)
	span, err := startSpan(ctx, tracer, Header(ctx), options)
	if err != nil {
		return nil, nil, err
	}
	return span, tracer.ContextWithSpan(context.WithValue(ctx, tracer.SpanContextKey(), span), span), nil
}

func startSpanFromWorkflowContext(
	ctx workflow.Context,
	tracer Tracer,
	options *TracerStartSpanOptions,
) (TracerSpan, workflow.Context, error) {
	span, err := startSpan(ctx, tracer, WorkflowHeader(ctx), options)
	if err != nil {
		return nil, nil, err
	}
	return span, workflow.WithValue(ctx, tracer.SpanContextKey(), span), nil
}

// Note, this does not put the span on the context
func startSpan(
	ctx interface{ Value(interface{}) interface{} },
	tracer Tracer,
	header map[string]*commonpb.Payload,
	options *TracerStartSpanOptions,
) (TracerSpan, error) {

	// Get parent span from header if not already present and allowed
	if options.Parent == nil && options.FromHeader {
		if span, err := readSpanFromHeader(tracer, header); err != nil {
			return nil, err
		} else if span != nil {
			options.Parent = span
		}
	}

	// If no parent span, try to get from context
	if options.Parent == nil {
		options.Parent, _ = ctx.Value(tracer.SpanContextKey()).(TracerSpan)
	}

	// Start the span
	span, err := tracer.StartSpan(options)
	if err != nil {
		return nil, err
	}

	// Put span in header if wanted
	if options.ToHeader && header != nil {
		if err := writeSpanToHeader(tracer, span, header); err != nil {
			return nil, err
		}
	}
	return span, nil
}

func readSpanFromHeader(tracer Tracer, header map[string]*commonpb.Payload) (TracerSpanRef, error) {
	// Get from map
	payload := header[tracer.HeaderKey()]
	if payload == nil {
		return nil, nil
	}
	// Convert from the payload
	var data map[string]string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &data); err != nil {
		return nil, err
	}
	// Unmarshal
	return tracer.UnmarshalSpan(data)
}

func writeSpanToHeader(tracer Tracer, span TracerSpan, header map[string]*commonpb.Payload) error {
	// Serialize span to map
	data, err := tracer.MarshalSpan(span)
	if err != nil {
		return err
	}
	// Convert to payload
	payload, err := converter.GetDefaultDataConverter().ToPayload(data)
	if err != nil {
		return err
	}
	// Put on header
	header[tracer.HeaderKey()] = payload
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