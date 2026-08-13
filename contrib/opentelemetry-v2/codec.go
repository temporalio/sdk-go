package opentelemetry

import (
	"context"
	"maps"
	"slices"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/log"
)

type textMapCarrier map[string]string

func (t textMapCarrier) Get(key string) string        { return t[key] }
func (t textMapCarrier) Set(key string, value string) { t[key] = value }
func (t textMapCarrier) Keys() []string               { return slices.Collect(maps.Keys(t)) }

type spanCodec struct {
	tracing.BaseTracer
	options *options
}

func (c *spanCodec) MarshalSpan(span tracing.TracerSpan) (map[string]string, error) {
	tSpan := asTracerSpan(span)
	if tSpan == nil {
		return nil, nil
	}

	ctx := context.Background()

	if tSpan.Span != nil && tSpan.Span.SpanContext().IsValid() {
		ctx = trace.ContextWithSpan(ctx, tSpan.Span)
	}

	if !c.options.DisableBaggage && tSpan.Baggage.Len() > 0 {
		ctx = baggage.ContextWithBaggage(ctx, tSpan.Baggage)
	}

	data := textMapCarrier{}
	c.options.TextMapPropagator.Inject(ctx, data)
	return data, nil
}

func (c *spanCodec) UnmarshalSpan(m map[string]string) (tracing.TracerSpanRef, error) {
	carrier := textMapCarrier(m)
	ctx := c.options.TextMapPropagator.Extract(context.Background(), carrier)

	spanRef := &tracerSpanRef{}

	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		spanRef.SpanContext = spanCtx
	}

	bag := baggage.FromContext(ctx)
	if !c.options.DisableBaggage && bag.Len() > 0 {
		spanRef.Baggage = bag
	}

	return spanRef, nil
}

func (c *spanCodec) GetLogger(logger log.Logger, ref tracing.TracerSpanRef) log.Logger {
	span := asTracerSpan(ref)
	if span == nil || !span.SpanContext().IsValid() {
		return logger
	}

	logger = log.With(logger,
		"TraceID", span.SpanContext().TraceID(),
		"SpanID", span.SpanContext().SpanID(),
	)

	return logger
}
