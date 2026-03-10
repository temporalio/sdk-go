package interceptor_test

import (
	"context"
	"testing"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/internal/interceptortest"
)

type testTracer struct {
	interceptor.BaseTracer
	T *testing.T
}

type testSpan struct{}

func (t testSpan) Finish(options *interceptor.TracerFinishSpanOptions) {}

type testSpanRef struct{}

func (t testTracer) Options() interceptor.TracerOptions {
	return interceptor.TracerOptions{
		SpanContextKey:       "test-tracer",
		HeaderKey:            "test-tracer",
		DisableSignalTracing: false,
		DisableQueryTracing:  false,
	}
}

func (t *testTracer) UnmarshalSpan(m map[string]string) (interceptor.TracerSpanRef, error) {
	return testSpanRef{}, nil
}

func (t *testTracer) MarshalSpan(span interceptor.TracerSpan) (map[string]string, error) {
	return map[string]string{}, nil
}

func (t *testTracer) SpanFromContext(ctx context.Context) interceptor.TracerSpan {
	return testSpan{}
}

func (t *testTracer) ContextWithSpan(ctx context.Context, span interceptor.TracerSpan) context.Context {
	return ctx
}

func (t *testTracer) StartSpan(options *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {
	// Require start time to be set
	if options.Time.IsZero() {
		switch options.Operation {
		case "RunWorkflow", "RunActivity":
		// Do nothing; the test env doesn't set these at the moment.
		default:
			t.T.Errorf("Got zero value for span start time: %v", options)
		}
	}
	return testSpan{}, nil
}

func TestSpanTimestamps(t *testing.T) {
	interceptortest.RunTestWorkflow(t, &testTracer{T: t})
}
