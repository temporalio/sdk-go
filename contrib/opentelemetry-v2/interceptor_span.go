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

// tracerSpanRef is a parent extracted from headers: span context + baggage, no
// live span to End/Finish.
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

	// Benign application errors are recorded but do not flip the span to Error.
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

func parentFromRef(ref tracing.TracerSpanRef) parentContext {
	switch p := ref.(type) {
	case *tracerSpan:
		return parentContext{spanContext: p.SpanContext(), baggage: p.Baggage}
	case *tracerSpanRef:
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

// buildSpan starts an OTel span. Non-empty key is placed on the start context
// for the deterministic ID generator; empty key yields a random id.
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
