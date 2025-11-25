package tracing

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/log"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type testTracer struct {
	interceptor.Tracer
	mt mocktracer.Tracer
}

func (t testTracer) SpanName(options *interceptor.TracerStartSpanOptions) string {
	return t.Tracer.(*tracerImpl).SpanName(options)
}

func (t testTracer) FinishedSpans() []*interceptortest.SpanInfo {
	return spanChildren(t.mt.FinishedSpans(), 0)
}

func spanChildren(spans []*mocktracer.Span, parentId uint64) (ret []*interceptortest.SpanInfo) {
	for _, s := range spans {
		if s.ParentID() == parentId {
			spanName := s.OperationName()
			ret = append(ret, interceptortest.Span(spanName, spanChildren(spans, s.SpanID())...))
		}
	}
	return
}

func TestSpanPropagation(t *testing.T) {
	// Start the mock tracer.
	mt := mocktracer.Start()
	defer mt.Stop()
	impl := NewTracer(TracerOptions{})
	testTracer := testTracer{
		Tracer: impl,
		mt:     mt,
	}
	interceptortest.RunTestWorkflow(t, testTracer)
	interceptortest.AssertSpanPropagation(t, testTracer)
}
func TestSpanName(t *testing.T) {
	// Start the mock tracer.
	mt := mocktracer.Start()
	defer mt.Stop()
	impl := NewTracer(TracerOptions{})
	testTracer := testTracer{
		Tracer: impl,
		mt:     mt,
	}
	interceptortest.RunTestWorkflow(t, testTracer)
	// Ensure the naming scheme follows "temporal.${operation}"
	require.Equal(t, "temporal.ValidateUpdate", testTracer.FinishedSpans()[0].Name)
	require.Equal(t, "temporal.HandleUpdate", testTracer.FinishedSpans()[1].Name)
	require.Equal(t, "temporal.RunWorkflow", testTracer.FinishedSpans()[2].Name)

}
func Test_tracerImpl_genSpanID(t1 *testing.T) {
	tests := []struct {
		name  string
		runId string
		want  uint64
	}{
		{
			name:  "Test always the same",
			runId: "afd160fc-2591-42fa-ad33-3c8f80084961",
			want:  11306945927996332141,
		},
		{
			name:  "Different runId",
			runId: "0",
			want:  12638153115695167471,
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			// Ensure that if we generate spans for two different consecutive runs, they are consistent
			// given the same input parameters (runId)
			if first := genSpanID(tt.runId); first != tt.want {
				t1.Errorf("genSpanID() = %v, want %v", first, tt.want)
				if second := genSpanID(tt.runId); second != first {
					t1.Errorf("first genSpanID() = %v, second genSpanID() = %v. Subsequent invocations MUST return the same result", first, second)
				}
			}
		})
	}
}
func Test_OnFinishOption(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	onFinish := func(options *interceptor.TracerFinishSpanOptions) []tracer.FinishOption {
		var finishOpts []tracer.FinishOption

		if err := options.Error; strings.Contains(err.Error(), "ignore me") {
			finishOpts = append(finishOpts, tracer.WithError(err))
		}

		return finishOpts
	}

	impl := NewTracer(TracerOptions{OnFinish: onFinish})
	trc := testTracer{
		Tracer: impl,
		mt:     mt,
	}
	interceptortest.RunTestWorkflowWithError(t, trc)

	spans := trc.FinishedSpans()

	require.Len(t, spans, 1)
	require.Equal(t, "temporal.RunWorkflow", spans[0].Name)
}

func setCustomSpanTagWorkflow(ctx workflow.Context) error {
	span, ok := SpanFromWorkflowContext(ctx)

	if !ok {
		return errors.New("Did not find span in workflow context")
	}

	span.SetTag("testTag", "testValue")
	return nil
}

func Test_SpanFromWorkflowContext(t *testing.T) {
	// Start the mock tracer.
	mt := mocktracer.Start()
	defer mt.Stop()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(setCustomSpanTagWorkflow)

	impl := NewTracer(TracerOptions{})
	testTracer := testTracer{
		Tracer: impl,
		mt:     mt,
	}

	// Set tracer interceptor
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{interceptor.NewTracingInterceptor(testTracer)},
	})

	env.ExecuteWorkflow(setCustomSpanTagWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	testSpan := mt.FinishedSpans()[0]
	require.Equal(t, "testValue", testSpan.Tag("testTag"))
}

// mockTracerSpan implements interceptor.TracerSpan but is not *tracerSpan
// Used to test type assertion error handling
type mockTracerSpan struct{}

func (m *mockTracerSpan) SpanID() uint64                               { return 0 }
func (m *mockTracerSpan) TraceID() uint64                              { return 0 }
func (m *mockTracerSpan) ForeachBaggageItem(func(string, string) bool) {}
func (m *mockTracerSpan) Finish(*interceptor.TracerFinishSpanOptions)  {}

func TestNewTracer(t *testing.T) {
	t.Run("with default OnFinish", func(t *testing.T) {
		opts := TracerOptions{
			DisableSignalTracing: true,
			DisableQueryTracing:  true,
		}
		tr := NewTracer(opts)
		require.NotNil(t, tr)

		impl, ok := tr.(*tracerImpl)
		require.True(t, ok)
		assert.True(t, impl.opts.DisableSignalTracing)
		assert.True(t, impl.opts.DisableQueryTracing)
		assert.NotNil(t, impl.opts.OnFinish)
	})

	t.Run("with custom OnFinish", func(t *testing.T) {
		customOnFinish := func(options *interceptor.TracerFinishSpanOptions) []tracer.FinishOption {
			return []tracer.FinishOption{tracer.WithError(errors.New("custom error"))}
		}
		opts := TracerOptions{
			OnFinish: customOnFinish,
		}
		tr := NewTracer(opts)
		require.NotNil(t, tr)

		impl, ok := tr.(*tracerImpl)
		require.True(t, ok)
		// Verify OnFinish is set (can't compare function pointers directly)
		assert.NotNil(t, impl.opts.OnFinish)
	})

	t.Run("with all options", func(t *testing.T) {
		opts := TracerOptions{
			DisableSignalTracing: true,
			DisableQueryTracing:  true,
			DisableUpdateTracing: true,
		}
		tr := NewTracer(opts)
		require.NotNil(t, tr)

		impl, ok := tr.(*tracerImpl)
		require.True(t, ok)
		assert.True(t, impl.opts.DisableSignalTracing)
		assert.True(t, impl.opts.DisableQueryTracing)
		assert.True(t, impl.opts.DisableUpdateTracing)
	})
}

func TestTracerImpl_Options(t *testing.T) {
	tr := NewTracer(TracerOptions{
		DisableSignalTracing: true,
		DisableQueryTracing:  true,
		DisableUpdateTracing: true,
	})

	impl, ok := tr.(*tracerImpl)
	require.True(t, ok)

	opts := impl.Options()
	assert.Equal(t, activeSpanContextKey, opts.SpanContextKey)
	assert.Equal(t, headerKey, opts.HeaderKey)
	assert.True(t, opts.DisableSignalTracing)
	assert.True(t, opts.DisableQueryTracing)
	assert.True(t, opts.DisableUpdateTracing)
}

func TestTracerImpl_UnmarshalSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tracerInstance := NewTracer(TracerOptions{})
	impl, ok := tracerInstance.(*tracerImpl)
	require.True(t, ok)

	t.Run("with valid span context", func(t *testing.T) {
		// Start a span and extract its context
		span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")
		defer span.Finish()

		carrier := tracer.TextMapCarrier{}
		err := tracer.Inject(span.Context(), carrier)
		require.NoError(t, err)

		ref, err := impl.UnmarshalSpan(carrier)
		require.NoError(t, err)
		require.NotNil(t, ref)

		spanCtx, ok := ref.(*tracerSpanCtx)
		require.True(t, ok)
		assert.NotNil(t, spanCtx.SpanContext)
	})

	t.Run("with no span context", func(t *testing.T) {
		emptyMap := make(map[string]string)
		ref, err := impl.UnmarshalSpan(emptyMap)
		assert.NoError(t, err)
		assert.Nil(t, ref)
	})

	t.Run("with invalid carrier", func(t *testing.T) {
		invalidMap := map[string]string{
			"invalid": "data",
		}
		ref, err := impl.UnmarshalSpan(invalidMap)
		// Should return nil ref without error for missing span context
		assert.NoError(t, err)
		assert.Nil(t, ref)
	})
}

func TestTracerImpl_MarshalSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tracerInstance := NewTracer(TracerOptions{})
	impl, ok := tracerInstance.(*tracerImpl)
	require.True(t, ok)

	t.Run("with valid span", func(t *testing.T) {
		span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")
		defer span.Finish()

		tracerSpan := &tracerSpan{
			Span:     span,
			OnFinish: nil,
		}

		marshaled, err := impl.MarshalSpan(tracerSpan)
		require.NoError(t, err)
		require.NotNil(t, marshaled)
		assert.NotEmpty(t, marshaled)
	})

	t.Run("verify round trip", func(t *testing.T) {
		span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")
		defer span.Finish()

		tracerSpan := &tracerSpan{
			Span:     span,
			OnFinish: nil,
		}

		marshaled, err := impl.MarshalSpan(tracerSpan)
		require.NoError(t, err)

		unmarshaled, err := impl.UnmarshalSpan(marshaled)
		require.NoError(t, err)
		require.NotNil(t, unmarshaled)
	})

	t.Run("with invalid span type", func(t *testing.T) {
		invalidSpan := &mockTracerSpan{}

		marshaled, err := impl.MarshalSpan(invalidSpan)
		assert.Error(t, err)
		assert.Nil(t, marshaled)
		assert.Contains(t, err.Error(), "expected *tracerSpan")
	})
}

func TestTracerImpl_SpanFromContext(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tracerInstance := NewTracer(TracerOptions{})
	impl, ok := tracerInstance.(*tracerImpl)
	require.True(t, ok)

	t.Run("with span in context", func(t *testing.T) {
		span, ctx := tracer.StartSpanFromContext(context.Background(), "test.operation")
		defer span.Finish()

		spanRef := impl.SpanFromContext(ctx)

		require.NotNil(t, spanRef)
		ts, ok := spanRef.(*tracerSpan)
		require.True(t, ok)
		assert.Equal(t, span, ts.Span)
	})

	t.Run("without span in context", func(t *testing.T) {
		ctx := context.Background()
		tracerSpan := impl.SpanFromContext(ctx)
		assert.Nil(t, tracerSpan)
	})
}

func TestTracerImpl_ContextWithSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tracerInstance := NewTracer(TracerOptions{})
	impl, ok := tracerInstance.(*tracerImpl)
	require.True(t, ok)

	span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")
	defer span.Finish()

	tracerSpan := &tracerSpan{
		Span:     span,
		OnFinish: nil,
	}

	t.Run("with valid span", func(t *testing.T) {
		ctx := impl.ContextWithSpan(context.Background(), tracerSpan)

		// Verify span is in context
		ctxSpan, ok := tracer.SpanFromContext(ctx)
		require.True(t, ok)
		assert.Equal(t, span, ctxSpan)
	})

	t.Run("with invalid span type", func(t *testing.T) {
		invalidSpan := &mockTracerSpan{}

		originalCtx := context.Background()
		resultCtx := impl.ContextWithSpan(originalCtx, invalidSpan)

		// Should return original context when span type is invalid
		assert.Equal(t, originalCtx, resultCtx)

		// Verify no span was added to context
		_, ok := tracer.SpanFromContext(resultCtx)
		assert.False(t, ok)
	})
}

func TestTracerImpl_StartSpan(t *testing.T) {
	tracerInstance := NewTracer(TracerOptions{})
	impl, ok := tracerInstance.(*tracerImpl)
	require.True(t, ok)

	t.Run("root span without parent", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		mt.Reset()
		startTime := time.Now().Round(0)
		opts := &interceptor.TracerStartSpanOptions{
			Operation: "test_operation",
			Name:      "Test Operation",
			Time:      startTime,
			Tags: map[string]string{
				"key1": "value1",
			},
		}

		span, err := impl.StartSpan(opts)
		require.NoError(t, err)
		require.NotNil(t, span)

		ts, ok := span.(*tracerSpan)
		require.True(t, ok)
		assert.NotNil(t, ts.Span)
		ts.Span.Finish()

		finishedSpans := mt.FinishedSpans()
		require.Len(t, finishedSpans, 1)
		mockSpan := finishedSpans[0]
		assert.Equal(t, "temporal.test_operation", mockSpan.OperationName())
		assert.Equal(t, "Test Operation", mockSpan.Tag("resource.name"))
		assert.Equal(t, startTime, mockSpan.StartTime())
		assert.Equal(t, "value1", mockSpan.Tag("temporal.key1"))
	})

	t.Run("span with parent tracerSpan", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		mt.Reset()

		parentSpan, _ := tracer.StartSpanFromContext(context.Background(), "parent.operation")
		parentTracerSpan := &tracerSpan{
			Span:     parentSpan,
			OnFinish: nil,
		}

		startTime := time.Now().Round(0)
		opts := &interceptor.TracerStartSpanOptions{
			Operation: "child_operation",
			Name:      "Child Operation",
			Time:      startTime,
			Parent:    parentTracerSpan,
			Tags: map[string]string{
				"key1": "value1",
			},
		}

		span, err := impl.StartSpan(opts)
		require.NoError(t, err)
		require.NotNil(t, span)

		ts, ok := span.(*tracerSpan)
		require.True(t, ok)
		assert.NotNil(t, ts.Span)
		ts.Span.Finish()
		parentSpan.Finish()

		// Verify the child span was captured with correct parent relationship
		finishedSpans := mt.FinishedSpans()
		require.Len(t, finishedSpans, 2)

		var childSpan *mocktracer.Span
		for _, s := range finishedSpans {
			if s.OperationName() == "temporal.child_operation" {
				childSpan = s
				break
			}
		}
		require.NotNil(t, childSpan)
		assert.Equal(t, "Child Operation", childSpan.Tag("resource.name"))
		assert.Equal(t, startTime, childSpan.StartTime())
		assert.Equal(t, "value1", childSpan.Tag("temporal.key1"))
		assert.Equal(t, parentSpan.Context().SpanID(), childSpan.ParentID())
		assert.Equal(t, parentSpan.Context().TraceIDLower(), childSpan.TraceID())
	})

	t.Run("span with parent tracerSpanCtx", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		mt.Reset()

		parentSpan, _ := tracer.StartSpanFromContext(context.Background(), "parent.operation")
		defer parentSpan.Finish()

		carrier := tracer.TextMapCarrier{}
		err := tracer.Inject(parentSpan.Context(), carrier)
		require.NoError(t, err)

		parentCtx, err := tracer.Extract(carrier)
		require.NoError(t, err)

		parentTracerSpanCtx := &tracerSpanCtx{
			SpanContext: parentCtx,
		}

		opts := &interceptor.TracerStartSpanOptions{
			Operation: "child_operation",
			Name:      "Child Operation",
			Time:      time.Now(),
			Parent:    parentTracerSpanCtx,
			Tags: map[string]string{
				"key1": "value1",
			},
		}

		span, err := impl.StartSpan(opts)
		require.NoError(t, err)
		require.NotNil(t, span)

		ts, ok := span.(*tracerSpan)
		require.True(t, ok)
		assert.NotNil(t, ts.Span)
		defer ts.Span.Finish()
	})

	t.Run("span with idempotency key", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		mt.Reset()

		idempotencyKey := "test-key-123"
		startTime := time.Now().Round(0)
		opts := &interceptor.TracerStartSpanOptions{
			Operation:      "test_operation",
			Name:           "Test Operation",
			Time:           startTime,
			IdempotencyKey: idempotencyKey,
		}

		span, err := impl.StartSpan(opts)
		require.NoError(t, err)
		require.NotNil(t, span)

		ts, ok := span.(*tracerSpan)
		require.True(t, ok)
		assert.NotNil(t, ts.Span)

		// Verify deterministic span ID before finishing
		expectedSpanID := genSpanID(idempotencyKey)
		assert.Equal(t, expectedSpanID, ts.Span.Context().SpanID())

		ts.Span.Finish()

		finishedSpans := mt.FinishedSpans()
		require.Len(t, finishedSpans, 1)
		mockSpan := finishedSpans[0]
		assert.Equal(t, "temporal.test_operation", mockSpan.OperationName())
		assert.Equal(t, expectedSpanID, mockSpan.SpanID())
		assert.Equal(t, startTime, mockSpan.StartTime())
	})

	t.Run("span with temporal prefixed tags", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		mt.Reset()

		startTime := time.Now().Round(0)
		opts := &interceptor.TracerStartSpanOptions{
			Operation: "test_operation",
			Name:      "Test Operation",
			Time:      startTime,
			Tags: map[string]string{
				"temporal.key1": "value1",
				"key2":          "value2",
			},
		}

		span, err := impl.StartSpan(opts)
		require.NoError(t, err)
		require.NotNil(t, span)

		ts, ok := span.(*tracerSpan)
		require.True(t, ok)
		ts.Span.Finish()

		// Verify tags are set correctly
		finishedSpans := mt.FinishedSpans()
		require.Len(t, finishedSpans, 1)
		mockSpan := finishedSpans[0]
		allTags := mockSpan.Tags()
		// Tags already prefixed with "temporal." should remain unchanged
		// Tags without a prefix should get "temporal." prefix added
		assert.Equal(t, "value1", allTags["temporal.key1"])
		assert.Equal(t, "value2", allTags["temporal.key2"])
	})

	t.Run("span with unknown parent type", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		mt.Reset()

		startTime := time.Now().Round(0)
		opts := &interceptor.TracerStartSpanOptions{
			Operation: "test_operation",
			Name:      "Test Operation",
			Time:      startTime,
			Parent:    "invalid parent type",
		}

		span, err := impl.StartSpan(opts)
		require.NoError(t, err)
		require.NotNil(t, span)

		ts, ok := span.(*tracerSpan)
		require.True(t, ok)
		ts.Span.Finish()

		// Verify span was created as root span (unknown parent should be ignored)
		finishedSpans := mt.FinishedSpans()
		require.Len(t, finishedSpans, 1)
		mockSpan := finishedSpans[0]
		assert.Equal(t, "temporal.test_operation", mockSpan.OperationName())
		assert.Equal(t, startTime, mockSpan.StartTime())
		// Should be a root span (no parent)
		assert.Zero(t, mockSpan.ParentID())
	})
}

func TestTracerImpl_SpanName(t *testing.T) {
	tr := NewTracer(TracerOptions{})
	impl, ok := tr.(*tracerImpl)
	require.True(t, ok)

	tests := []struct {
		name      string
		operation string
		expected  string
	}{
		{
			name:      "simple operation",
			operation: "workflow_execution",
			expected:  "temporal.workflow_execution",
		},
		{
			name:      "activity operation",
			operation: "activity_execution",
			expected:  "temporal.activity_execution",
		},
		{
			name:      "empty operation",
			operation: "",
			expected:  "temporal.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &interceptor.TracerStartSpanOptions{
				Operation: tt.operation,
			}
			result := impl.SpanName(opts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTracerImpl_GetLogger(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tracerInstance := NewTracer(TracerOptions{})
	impl, ok := tracerInstance.(*tracerImpl)
	require.True(t, ok)

	t.Run("with valid tracerSpan", func(t *testing.T) {
		span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")
		defer span.Finish()

		tracerSpan := &tracerSpan{
			Span:     span,
			OnFinish: nil,
		}

		logger := log.NewStructuredLogger(slog.Default())
		enhancedLogger := impl.GetLogger(logger, tracerSpan)

		require.NotNil(t, enhancedLogger)
		// Logger should be enhanced with trace ID and span ID
		assert.NotEqual(t, logger, enhancedLogger)
	})

	t.Run("with invalid ref type", func(t *testing.T) {
		logger := log.NewStructuredLogger(slog.Default())
		invalidRef := &tracerSpanCtx{}

		enhancedLogger := impl.GetLogger(logger, invalidRef)

		// Should return original logger when ref type is invalid
		assert.Equal(t, logger, enhancedLogger)
	})

	t.Run("with nil ref", func(t *testing.T) {
		logger := log.NewStructuredLogger(slog.Default())
		enhancedLogger := impl.GetLogger(logger, nil)

		// Should return original logger when ref is nil
		assert.Equal(t, logger, enhancedLogger)
	})
}

func TestTracerSpan_Finish(t *testing.T) {
	t.Run("with error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")

		onFinishCalled := false
		customOnFinish := func(options *interceptor.TracerFinishSpanOptions) []tracer.FinishOption {
			onFinishCalled = true
			return []tracer.FinishOption{tracer.WithError(options.Error)}
		}

		tracerSpan := &tracerSpan{
			Span:     span,
			OnFinish: customOnFinish,
		}

		err := errors.New("test error")
		finishOpts := &interceptor.TracerFinishSpanOptions{
			Error: err,
		}

		tracerSpan.Finish(finishOpts)
		assert.True(t, onFinishCalled)

		// Verify error was set on the span
		finishedSpans := mt.FinishedSpans()
		require.Len(t, finishedSpans, 1)
		mockSpan := finishedSpans[0]
		errorTag := mockSpan.Tag("error")
		if errorTag != nil {
			assert.True(t, errorTag.(bool))
		}
		assert.Equal(t, "test error", mockSpan.Tag("error.message"))
	})

	t.Run("with continue as new error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")

		onFinishCalled := false
		customOnFinish := func(options *interceptor.TracerFinishSpanOptions) []tracer.FinishOption {
			onFinishCalled = true
			if err := options.Error; err != nil && !workflow.IsContinueAsNewError(err) {
				return []tracer.FinishOption{tracer.WithError(err)}
			}
			return nil
		}

		tracerSpan := &tracerSpan{
			Span:     span,
			OnFinish: customOnFinish,
		}

		// Create a test workflow environment to get a proper ContinueAsNewError
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return workflow.NewContinueAsNewError(ctx, "continue")
		})

		require.Error(t, env.GetWorkflowError())
		contAsNewErr := env.GetWorkflowError()

		finishOpts := &interceptor.TracerFinishSpanOptions{
			Error: contAsNewErr,
		}

		tracerSpan.Finish(finishOpts)
		assert.True(t, onFinishCalled)
	})

	t.Run("without error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")

		onFinishCalled := false
		customOnFinish := func(options *interceptor.TracerFinishSpanOptions) []tracer.FinishOption {
			onFinishCalled = true
			return nil
		}

		tracerSpan := &tracerSpan{
			Span:     span,
			OnFinish: customOnFinish,
		}

		finishOpts := &interceptor.TracerFinishSpanOptions{}
		tracerSpan.Finish(finishOpts)
		assert.True(t, onFinishCalled)

		// Verify span finished without error
		finishedSpans := mt.FinishedSpans()
		require.Len(t, finishedSpans, 1)
		mockSpan := finishedSpans[0]
		assert.Nil(t, mockSpan.Tag("error"))
	})
}

func TestTracerSpan_SpanID(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")
	defer span.Finish()

	tracerSpan := &tracerSpan{
		Span:     span,
		OnFinish: nil,
	}

	spanID := tracerSpan.SpanID()
	assert.Equal(t, span.Context().SpanID(), spanID)
}

func TestTracerSpan_TraceID(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	span, _ := tracer.StartSpanFromContext(context.Background(), "test.operation")
	defer span.Finish()

	tracerSpan := &tracerSpan{
		Span:     span,
		OnFinish: nil,
	}

	traceID := tracerSpan.TraceID()
	assert.Equal(t, span.Context().TraceID(), traceID)
}

func TestGenSpanID(t *testing.T) {
	tests := []struct {
		name           string
		idempotencyKey string
	}{
		{
			name:           "simple key",
			idempotencyKey: "test-key",
		},
		{
			name:           "empty key",
			idempotencyKey: "",
		},
		{
			name:           "long key",
			idempotencyKey: "very-long-idempotency-key-with-many-characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spanID1 := genSpanID(tt.idempotencyKey)
			spanID2 := genSpanID(tt.idempotencyKey)

			// Should be deterministic
			assert.Equal(t, spanID1, spanID2)
			assert.NotZero(t, spanID1)
		})
	}

	t.Run("different keys produce different IDs", func(t *testing.T) {
		spanID1 := genSpanID("key1")
		spanID2 := genSpanID("key2")

		assert.NotEqual(t, spanID1, spanID2)
	})
}

func TestNewTracingInterceptor(t *testing.T) {
	opts := TracerOptions{
		DisableSignalTracing: true,
		DisableQueryTracing:  true,
	}

	i := NewTracingInterceptor(opts)
	require.NotNil(t, i)
}
