package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/temporal"
)

// tracerSpanRef is a parent context extracted from headers.
type tracerSpanRef struct {
	trace.SpanContext
	baggage.Baggage
}

type tracerSpan struct {
	trace.Span
	baggage.Baggage
}

func (t *tracerSpan) Finish(opts *tracing.TracerFinishSpanOptions) {
	t.RecordError(opts.Error)

	// Benign application errors do not mark spans as failed.
	if opts.Error != nil && !isBenignApplicationError(opts.Error) {
		t.SetStatus(codes.Error, opts.Error.Error())
	}
	t.End()
}

func isBenignApplicationError(err error) bool {
	appError, _ := err.(*temporal.ApplicationError)
	return appError != nil && appError.Category() == temporal.ApplicationErrorCategoryBenign
}

type parentContext struct {
	spanContext trace.SpanContext
	baggage     baggage.Baggage
}

// asTracerSpan unwraps a live tracerSpan. It returns nil for references and
// unknown span types.
func asTracerSpan(ref tracing.TracerSpanRef) *tracerSpan {
	switch p := ref.(type) {
	case *tracerSpan:
		return p
	case *interceptorWorkflowSpan:
		return p.tracerSpan
	case *workflowSpan:
		return p.tracerSpan
	}
	return nil
}

func parentFromRef(ref tracing.TracerSpanRef) parentContext {
	if span := asTracerSpan(ref); span != nil {
		return parentContext{spanContext: span.SpanContext(), baggage: span.Baggage}
	}
	if p, ok := ref.(*tracerSpanRef); ok {
		return parentContext{spanContext: p.SpanContext, baggage: p.Baggage}
	}
	return parentContext{}
}

func (c *tracerConfig) contextWithParent(ctx context.Context, parent parentContext) context.Context {
	if parent.spanContext.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, parent.spanContext)
	}
	if !c.options.DisableBaggage {
		ctx = baggage.ContextWithBaggage(ctx, parent.baggage)
	}
	return ctx
}

// buildSpan starts an OTel span, using key for deterministic IDs when set.
func (c *tracerConfig) buildSpan(
	otel trace.Tracer,
	parent parentContext,
	name string,
	key string,
	opts *tracing.TracerStartSpanOptions,
) *tracerSpan {
	parentCtx := c.contextWithParent(context.Background(), parent)
	parentCtx = context.WithValue(parentCtx, otelIdKey{}, key)

	var spanOpts []trace.SpanStartOption
	switch opts.Direction {
	case tracing.SpanDirectionInbound:
		spanOpts = append(spanOpts, trace.WithSpanKind(trace.SpanKindServer))
	case tracing.SpanDirectionOutbound:
		spanOpts = append(spanOpts, trace.WithSpanKind(trace.SpanKindClient))
	default:
		spanOpts = append(spanOpts, trace.WithSpanKind(trace.SpanKindUnspecified))
	}

	if len(opts.Tags) > 0 {
		attrs := make([]attribute.KeyValue, 0, len(opts.Tags))
		for k, v := range opts.Tags {
			attrs = append(attrs, attribute.String(k, v))
		}
		spanOpts = append(spanOpts, trace.WithAttributes(attrs...))
	}

	if !opts.Time.IsZero() {
		spanOpts = append(spanOpts, trace.WithTimestamp(opts.Time))
	}

	_, span := otel.Start(parentCtx, name, spanOpts...)

	tSpan := &tracerSpan{Span: span}
	if !c.options.DisableBaggage {
		tSpan.Baggage = parent.baggage
	}
	return tSpan
}
