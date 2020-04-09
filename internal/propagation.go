package internal

import (
	"context"
	"time"

	"github.com/opentracing/opentracing-go"
)

const (
	workflowTag = "temporalWorkflowID"

	runTag = "temporalRunID"
)

// createOpenTracingWorkflowSpan creates a new context with a workflow started span
func createOpenTracingWorkflowSpan(
	ctx context.Context,
	tracer opentracing.Tracer,
	start time.Time,
	workflowType, workflowID string,
) (context.Context, opentracing.Span) {
	tags := opentracing.Tags{
		workflowTag: workflowID,
	}
	return createOpenTracingSpan(ctx, tracer, start, workflowType, tags)
}

// createOpenTracingActivitySpan creates a new context with an activity started span
func createOpenTracingActivitySpan(
	ctx context.Context,
	tracer opentracing.Tracer,
	start time.Time,
	activityType, workflowID, runID string,
) (context.Context, opentracing.Span) {
	tags := opentracing.Tags{
		workflowTag: workflowID,
		runTag:      runID,
	}
	return createOpenTracingSpan(ctx, tracer, start, activityType, tags)
}

func createOpenTracingSpan(
	ctx context.Context,
	tracer opentracing.Tracer,
	start time.Time,
	name string,
	tags opentracing.Tags,
) (context.Context, opentracing.Span) {
	if _, ok := tracer.(opentracing.NoopTracer); ok {
		return ctx, tracer.StartSpan("StartWorkflow-Span")
	}

	var parent opentracing.SpanContext
	if parentSpan := opentracing.SpanFromContext(ctx); parentSpan != nil {
		parent = parentSpan.Context()
	} else if spanCtx, ok := ctx.Value(activeSpanContextKey).(opentracing.SpanContext); ok {
		parent = spanCtx
	}

	span := tracer.StartSpan(
		name,
		opentracing.StartTime(start),
		opentracing.FollowsFrom(parent),
		tags,
	)

	ctx = opentracing.ContextWithSpan(ctx, span)
	return ctx, span
}
