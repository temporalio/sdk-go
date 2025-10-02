package opentelemetry_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
	"go.temporal.io/sdk/temporal"
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
	tests := []struct {
		operation    string
		outbound     bool
		expectedKind trace.SpanKind
	}{
		{
			operation:    "StartWorkflow",
			outbound:     true,
			expectedKind: trace.SpanKindClient,
		},
		{
			operation:    "RunWorkflow",
			outbound:     false,
			expectedKind: trace.SpanKindServer,
		},
	}

	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			rec := tracetest.NewSpanRecorder()
			tracer, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
				Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer(""),
			})
			require.NoError(t, err)

			span, err := tracer.StartSpan(&interceptor.TracerStartSpanOptions{
				Operation: tt.operation,
				Name:      "test-span",
				Outbound:  tt.outbound,
			})
			require.NoError(t, err)

			span.Finish(&interceptor.TracerFinishSpanOptions{})

			spans := rec.Ended()
			require.Equal(t, len(spans), 1)

			foundSpan := spans[0]
			assert.Equal(t, tt.expectedKind, foundSpan.SpanKind(),
				"Expected span kind %v but got %v for operation %s (outbound=%v)",
				tt.expectedKind, foundSpan.SpanKind(), tt.operation, tt.outbound)
		})
	}
}

func TestBenignErrorSpanStatus(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectError  bool
		expectStatus codes.Code
	}{
		{
			name:         "benign application error should not set error status",
			err:          temporal.NewApplicationErrorWithOptions("benign error", "TestType", temporal.ApplicationErrorOptions{Category: temporal.ApplicationErrorCategoryBenign}),
			expectError:  false,
			expectStatus: codes.Unset,
		},
		{
			name:         "regular application error should set error status",
			err:          temporal.NewApplicationError("regular error", "TestType"),
			expectError:  true,
			expectStatus: codes.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := tracetest.NewSpanRecorder()
			tracer, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
				Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer(""),
			})
			require.NoError(t, err)

			span, err := tracer.StartSpan(&interceptor.TracerStartSpanOptions{
				Operation: "TestOperation",
				Name:      "TestSpan",
				Time:      time.Now(),
			})
			require.NoError(t, err)

			span.Finish(&interceptor.TracerFinishSpanOptions{
				Error: tt.err,
			})

			// Check recorded spans
			spans := rec.Ended()
			require.Len(t, spans, 1)

			recordedSpan := spans[0]
			assert.Equal(t, tt.expectStatus, recordedSpan.Status().Code)

			if tt.expectError {
				assert.NotEmpty(t, recordedSpan.Status().Description)
			} else {
				assert.Empty(t, recordedSpan.Status().Description)
			}
		})
	}
}
