// Copyright (c) 2017 Uber Technologies, Inc.
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

package internal

import (
	"context"
	"time"

	"github.com/opentracing/opentracing-go"
)

const (
	workflowTag = "cadenceWorkflowID"

	runTag = "cadenceRunID"
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
