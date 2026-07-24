package opentelemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func nopActivity(context.Context) error { return nil }

func spanKindWorkflow(ctx workflow.Context) error {
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	return workflow.ExecuteActivity(actx, nopActivity).Get(actx, nil)
}

func TestSpanKind(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ti := newTracingInterceptor(TracerOptions{}, provider)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{ti}})
	env.RegisterActivity(nopActivity)
	env.RegisterWorkflow(spanKindWorkflow)
	env.ExecuteWorkflow(spanKindWorkflow)
	require.NoError(t, env.GetWorkflowError())

	kinds := make(map[string]trace.SpanKind)
	for _, s := range recorder.Ended() {
		kinds[s.Name()] = s.SpanKind()
	}
	require.Equal(t, trace.SpanKindServer, kinds["RunWorkflow:spanKindWorkflow"])
	require.Equal(t, trace.SpanKindClient, kinds["StartActivity:nopActivity"])
	require.Equal(t, trace.SpanKindServer, kinds["RunActivity:nopActivity"])
}

func TestSpanErrorStatus(t *testing.T) {
	newEnv := func(t *testing.T) (*tracetest.SpanRecorder, *testsuite.TestWorkflowEnvironment) {
		t.Helper()
		recorder := tracetest.NewSpanRecorder()
		provider := NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		ti := newTracingInterceptor(TracerOptions{}, provider)
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{ti}})
		return recorder, env
	}

	t.Run("benign", func(t *testing.T) {
		recorder, env := newEnv(t)
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return temporal.NewApplicationErrorWithOptions("expected error", "BenignError",
				temporal.ApplicationErrorOptions{Category: temporal.ApplicationErrorCategoryBenign})
		})
		require.Error(t, env.GetWorkflowError())
		spans := recorder.Ended()
		require.Len(t, spans, 1)
		require.Equal(t, codes.Unset, spans[0].Status().Code)
	})

	t.Run("error", func(t *testing.T) {
		recorder, env := newEnv(t)
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return errors.New("unexpected error")
		})
		require.Error(t, env.GetWorkflowError())
		spans := recorder.Ended()
		require.Len(t, spans, 1)
		require.Equal(t, codes.Error, spans[0].Status().Code)
	})
}
