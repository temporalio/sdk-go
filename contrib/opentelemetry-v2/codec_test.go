package opentelemetry

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestSpanCodecUnmarshalSpanCustomPropagator(t *testing.T) {
	codec := spanCodec{tracerConfig: newTracerConfig(TracerOptions{
		TextMapPropagator: b3TestPropagator{},
	})}
	expected := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
		Remote:  true,
	})

	ref, err := codec.UnmarshalSpan(map[string]string{
		"b3": expected.TraceID().String() + "-" + expected.SpanID().String(),
	})
	require.NoError(t, err)
	require.Equal(t, expected, ref.(*tracerSpanRef).SpanContext)
}

func TestSpanCodecUnmarshalSpanWithoutTraceHeader(t *testing.T) {
	codec := spanCodec{tracerConfig: newTracerConfig(TracerOptions{})}

	ref, err := codec.UnmarshalSpan(map[string]string{"baggage": "key=value"})
	require.NoError(t, err)
	require.Nil(t, ref)
}

type b3TestPropagator struct{}

func (b3TestPropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		carrier.Set("b3", spanContext.TraceID().String()+"-"+spanContext.SpanID().String())
	}
}

func (b3TestPropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	parts := strings.Split(carrier.Get("b3"), "-")
	if len(parts) != 2 {
		return ctx
	}
	traceID, traceErr := trace.TraceIDFromHex(parts[0])
	spanID, spanErr := trace.SpanIDFromHex(parts[1])
	if traceErr != nil || spanErr != nil {
		return ctx
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
		Remote:  true,
	})
	return trace.ContextWithRemoteSpanContext(ctx, spanContext)
}

func (b3TestPropagator) Fields() []string {
	return []string{"b3"}
}
