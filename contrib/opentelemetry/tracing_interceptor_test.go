package opentelemetry_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
)

func TestSpanPropagation(t *testing.T) {
	var rec tracetest.SpanRecorder
	tracer, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
		Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(&rec)).Tracer(""),
	})
	require.NoError(t, err)

	testTracer := &testTracer{Tracer: tracer, rec: &rec}
	interceptortest.RunTestWorkflow(t, testTracer)
	interceptortest.AssertSpanPropagation(t, testTracer)
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

func TestSpanKind(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tracer, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
		Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer(""),
	})
	require.NoError(t, err)

	SpanTest(t, tracer, rec, "StartWorkflow", true, trace.SpanKindClient)
	SpanTest(t, tracer, rec, "RunWorkflow", false, trace.SpanKindServer)

}

func SpanTest(t *testing.T, tracer interceptor.Tracer, rec *tracetest.SpanRecorder, operation string, outbound bool, expectedKind trace.SpanKind) {
	span, err := tracer.StartSpan(&interceptor.TracerStartSpanOptions{
		Operation: operation,
		Name:      "test-span",
		Outbound:  outbound,
	})
	require.NoError(t, err)

	span.Finish(&interceptor.TracerFinishSpanOptions{})

	spans := rec.Ended()
	require.GreaterOrEqual(t, len(spans), 1)

	foundSpan := spans[len(spans)-1]
	assert.Equal(t, expectedKind, foundSpan.SpanKind(),
		"Expected span kind %v but got %v for operation %s (outbound=%v)",
		expectedKind, foundSpan.SpanKind(), operation, outbound)
}
