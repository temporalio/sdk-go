package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/workflow"
)

type spanContextKey struct{}

// contextBridge stores/loads TracerSpans on a standard context.Context via the
// OTel active-span APIs (and baggage when enabled).
type contextBridge struct {
	tracerConfig
}

func (b *contextBridge) SpanFromContext(ctx context.Context) tracing.TracerSpan {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return nil
	}
	tSpan := &tracerSpan{Span: span}
	if !b.options.DisableBaggage {
		tSpan.Baggage = baggage.FromContext(ctx)
	}
	return tSpan
}

func (b *contextBridge) ContextWithSpan(ctx context.Context, span tracing.TracerSpan) context.Context {
	tSpan, ok := span.(*tracerSpan)
	if !ok || tSpan == nil {
		return ctx
	}

	if !b.options.DisableBaggage {
		ctx = baggage.ContextWithBaggage(ctx, tSpan.Baggage)
	}

	return trace.ContextWithSpan(ctx, tSpan.Span)
}

// workflowContextBridge stores/loads TracerSpans on workflow.Context. Unlike
// context.Context, workflow.Context has no OTel span APIs, so spans are kept
// under spanContextKey.
type workflowContextBridge struct{}

func (workflowContextBridge) SpanFromContext(ctx workflow.Context) tracing.TracerSpan {
	span, _ := ctx.Value(spanContextKey{}).(tracing.TracerSpan)
	return span
}

func (workflowContextBridge) ContextWithSpan(ctx workflow.Context, span tracing.TracerSpan) workflow.Context {
	if span == nil {
		return ctx
	}
	return workflow.WithValue(ctx, spanContextKey{}, span)
}
