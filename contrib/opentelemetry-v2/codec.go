package opentelemetry

import (
	"context"
	"fmt"

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
	tSpan, ok := span.(*tracerSpan)
	if !ok || tSpan == nil {
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
	// No W3C traceparent means there is simply no parent span in the headers.
	if _, ok := m["traceparent"]; !ok {
		return nil, nil
	}
	ctx := c.options.TextMapPropagator.Extract(context.Background(), textMapCarrier(m))
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return nil, fmt.Errorf("failed extracting OpenTelemetry span from map")
	}
	spanRef := &tracerSpanRef{SpanContext: spanCtx}
	if !c.options.DisableBaggage {
		spanRef.Baggage = baggage.FromContext(ctx)
	}
	return spanRef, nil
}

func (c *spanCodec) GetLogger(logger log.Logger, ref tracing.TracerSpanRef) log.Logger {
	span, ok := ref.(*tracerSpan)
	if !ok {
		return logger
	}

	logger = log.With(logger,
		"TraceID", span.SpanContext().TraceID(),
		"SpanID", span.SpanContext().SpanID(),
	)

	return logger
}
