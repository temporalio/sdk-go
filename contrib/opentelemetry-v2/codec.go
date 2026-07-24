package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/log"
)

type textMapCarrier map[string]string

func (t textMapCarrier) Get(key string) string        { return t[key] }
func (t textMapCarrier) Set(key string, value string) { t[key] = value }
func (t textMapCarrier) Keys() []string {
	ret := make([]string, 0, len(t))
	for k := range t {
		ret = append(ret, k)
	}
	return ret
}

type spanCodec struct {
	tracing.BaseTracer
	tracerConfig
}

func (c *spanCodec) MarshalSpan(span tracing.TracerSpan) (map[string]string, error) {
	tSpan := asTracerSpan(span)
	if tSpan == nil {
		return nil, nil
	}

	data := textMapCarrier{}
	ctx := context.Background()
	if !c.options.DisableBaggage {
		ctx = baggage.ContextWithBaggage(ctx, tSpan.Baggage)
	}
	c.options.TextMapPropagator.Inject(trace.ContextWithSpan(ctx, tSpan.Span), data)
	return data, nil
}

func (c *spanCodec) UnmarshalSpan(m map[string]string) (tracing.TracerSpanRef, error) {
	ctx := c.options.TextMapPropagator.Extract(context.Background(), textMapCarrier(m))
	spanCtx := trace.SpanContextFromContext(ctx)
	// No valid span context means there is simply no parent span in the headers,
	// regardless of which propagator/header format is in use.
	if !spanCtx.IsValid() {
		return nil, nil
	}
	spanRef := &tracerSpanRef{SpanContext: spanCtx}
	if !c.options.DisableBaggage {
		spanRef.Baggage = baggage.FromContext(ctx)
	}
	return spanRef, nil
}

func (c *spanCodec) GetLogger(logger log.Logger, ref tracing.TracerSpanRef) log.Logger {
	span := asTracerSpan(ref)
	if span == nil {
		return logger
	}

	logger = log.With(logger,
		"TraceID", span.SpanContext().TraceID(),
		"SpanID", span.SpanContext().SpanID(),
	)

	return logger
}
