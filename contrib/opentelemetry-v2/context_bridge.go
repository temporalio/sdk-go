package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/workflow"
)

type spanContextKey struct{}

// contextBridge stores spans and optional baggage on context.Context.
type contextBridge struct {
	options *options
}

func (b *contextBridge) SpanFromContext(ctx context.Context) tracing.TracerSpanRef {
	res := &tracerSpan{}

	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		res.Span = trace.SpanFromContext(ctx)
	} else {
		res.Span = noop.Span{}
	}

	bag := baggage.FromContext(ctx)
	if !b.options.DisableBaggage && bag.Len() > 0 {
		res.Baggage = bag
	}

	return res
}

func (b *contextBridge) ContextWithSpan(ctx context.Context, ref tracing.TracerSpanRef) context.Context {
	span := asTracerSpan(ref)
	if span != nil && span.Span != nil && span.SpanContext().IsValid() {
		ctx = trace.ContextWithSpan(ctx, span.Span)
	}

	if !b.options.DisableBaggage && span != nil && span.Baggage.Len() > 0 {
		ctx = baggage.ContextWithBaggage(ctx, span.Baggage)
	}

	return ctx
}

// workflowContextBridge stores spans and optional baggage on workflow.Context under spanContextKey.
type workflowContextBridge struct {
	options *options
}

func (b *workflowContextBridge) SpanFromContext(ctx workflow.Context) tracing.TracerSpanRef {
	res := &tracerSpan{}

	span := asTracerSpan(ctx.Value(spanContextKey{}))
	if span != nil && span.Span != nil && span.SpanContext().IsValid() {
		res.Span = span.Span
	} else {
		res.Span = noop.Span{}
	}

	if !b.options.DisableBaggage && span != nil && span.Baggage.Len() > 0 {
		res.Baggage = span.Baggage
	}

	return res
}

func (b *workflowContextBridge) ContextWithSpan(ctx workflow.Context, ref tracing.TracerSpanRef) workflow.Context {
	newSpan := &tracerSpan{}

	currentSpan := b.SpanFromContext(ctx).(*tracerSpan)

	span := asTracerSpan(ref)
	if span != nil && span.Span != nil && span.SpanContext().IsValid() {
		newSpan.Span = span.Span
	} else {
		newSpan.Span = currentSpan.Span
	}

	if !b.options.DisableBaggage && span != nil && span.Baggage.Len() > 0 {
		newSpan.Baggage = span.Baggage
	} else {
		newSpan.Baggage = currentSpan.Baggage
	}

	ctx = workflow.WithValue(ctx, spanContextKey{}, newSpan)

	return ctx
}
