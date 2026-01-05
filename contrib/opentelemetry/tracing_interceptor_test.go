package opentelemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
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
		toHeader     bool
		fromHeader   bool
		expectedKind trace.SpanKind
	}{
		{
			operation:    "StartWorkflow",
			toHeader:     true,
			fromHeader:   false,
			expectedKind: trace.SpanKindClient,
		},
		{
			operation:    "RunWorkflow",
			toHeader:     false,
			fromHeader:   true,
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
				Operation:  tt.operation,
				Name:       "test-span",
				ToHeader:   tt.toHeader,
				FromHeader: tt.fromHeader,
			})
			require.NoError(t, err)

			span.Finish(&interceptor.TracerFinishSpanOptions{})

			spans := rec.Ended()
			require.Equal(t, len(spans), 1)

			foundSpan := spans[0]
			assert.Equal(t, tt.expectedKind, foundSpan.SpanKind(),
				"Expected span kind %v but got %v for operation %s",
				tt.expectedKind, foundSpan.SpanKind(), tt.operation)
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

func setCustomSpanAttrWorkflow(ctx workflow.Context) error {
	span, ok := opentelemetry.SpanFromWorkflowContext(ctx)
	if !ok {
		return errors.New("Did not find span in workflow context")
	}

	span.SetAttributes(attribute.String("testTag", "testValue"))
	return nil
}

func TestSpanFromWorkflowContext(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tracer, err := opentelemetry.NewTracer(opentelemetry.TracerOptions{
		Tracer: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)).Tracer(""),
	})
	require.NoError(t, err)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(setCustomSpanAttrWorkflow)

	// Set tracer interceptor
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{interceptor.NewTracingInterceptor(tracer)},
	})

	env.ExecuteWorkflow(setCustomSpanAttrWorkflow)

	require.True(t, env.IsWorkflowCompleted())

	// Verify span was recorded with added attribute
	spans := rec.Ended()
	require.GreaterOrEqual(t, len(spans), 1)

	found := false
	for _, s := range spans {
		for _, kv := range s.Attributes() {
			if string(kv.Key) == "testTag" && kv.Value.AsString() == "testValue" {
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	require.True(t, found, "expected to find attribute 'testTag=testValue' on recorded spans")
}

func TestSpanFromWorkflowContextNoOpSpan(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	nilValueWorkflow := func(ctx workflow.Context) error {
		span, ok := opentelemetry.SpanFromWorkflowContext(ctx)

		if ok {
			return errors.New("Expected ok to be false")
		}

		// Make sure we retain behavior of returning no-op span when no span is present in context
		noopSpan := trace.SpanFromContext(context.TODO())
		if span != noopSpan {
			return errors.New("Expected span to be no-op span")
		}

		return nil
	}

	env.RegisterWorkflow(nilValueWorkflow)
	env.ExecuteWorkflow(nilValueWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
