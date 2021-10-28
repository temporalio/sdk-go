package opentelemetry_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
	"go.temporal.io/sdk/opentelemetry"
)

func TestSpanPropagation(t *testing.T) {
	var rec tracetest.SpanRecorder
	tracer, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
		Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(&rec)).Tracer(""),
	})
	require.NoError(t, err)
	interceptortest.AssertSpanPropagation(t, &testTracer{Tracer: tracer, rec: &rec})
}

type testTracer struct {
	interceptor.Tracer
	rec *tracetest.SpanRecorder
}

func (t *testTracer) FinishedSpans() []*interceptortest.SpanInfo {
	return spanChildren(t.rec.Ended(), trace.SpanID{})
}

func spanChildren(spans []sdktrace.ReadOnlySpan, parentID trace.SpanID) (ret []*interceptortest.SpanInfo) {
	for _, s := range spans {
		if s.Parent().SpanID() == parentID {
			ret = append(ret, interceptortest.Span(s.Name(), spanChildren(spans, s.SpanContext().SpanID())...))
		}
	}
	return
}
