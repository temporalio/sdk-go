package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/workflow"
)

type spanContextKey struct{}

// contextBridge stores spans and optional baggage on context.Context.
type contextBridge struct {
	options *options
}

func (b *contextBridge) SpanFromContext(ctx context.Context) tracing.TracerSpan {
	// trace.SpanFromContext yields a no-op span when none is present.
	tSpan := &tracerSpan{Span: trace.SpanFromContext(ctx)}

	if bag := baggage.FromContext(ctx); !b.options.DisableBaggage && bag.Len() > 0 {
		tSpan.Baggage = bag
	}

	return tSpan
}

func (b *contextBridge) ContextWithSpan(ctx context.Context, span tracing.TracerSpan) context.Context {
	tSpan := asTracerSpan(span)
	if tSpan == nil {
		return ctx
	}

	if !b.options.DisableBaggage && tSpan.Baggage.Len() > 0 {
		ctx = baggage.ContextWithBaggage(ctx, tSpan.Baggage)
	}

	return trace.ContextWithSpan(ctx, tSpan.Span)
}

// workflowContextBridge stores spans on workflow.Context under spanContextKey.
type workflowContextBridge struct{}

func (workflowContextBridge) SpanFromContext(ctx workflow.Context) tracing.TracerSpan {
	span, _ := ctx.Value(spanContextKey{}).(tracing.TracerSpan)
	if asTracerSpan(span) == nil {
		// trace.SpanFromContext yields a no-op span when none is present.
		return &tracerSpan{Span: trace.SpanFromContext(context.Background())}
	}

	return span
}

func (workflowContextBridge) ContextWithSpan(ctx workflow.Context, span tracing.TracerSpan) workflow.Context {
	tSpan := asTracerSpan(span)
	if tSpan == nil {
		return ctx
	}

	return workflow.WithValue(ctx, spanContextKey{}, span)
}

type parentContext struct {
	spanContext trace.SpanContext
	baggage     baggage.Baggage
}

func parentContextFromRef(ref tracing.TracerSpanRef) parentContext {
	if span := asTracerSpan(ref); span != nil {
		return parentContext{spanContext: span.SpanContext(), baggage: span.Baggage}
	}

	if p, ok := ref.(*tracerSpanRef); ok {
		return parentContext{spanContext: p.SpanContext, baggage: p.Baggage}
	}

	return parentContext{}
}

func contextWithParent(ctx context.Context, parent tracing.TracerSpanRef, options *options) context.Context {
	parentContext := parentContextFromRef(parent)

	if parentContext.spanContext.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, parentContext.spanContext)
	}

	if options != nil && !options.DisableBaggage && parentContext.baggage.Len() > 0 {
		ctx = baggage.ContextWithBaggage(ctx, parentContext.baggage)
	}

	return ctx
}
