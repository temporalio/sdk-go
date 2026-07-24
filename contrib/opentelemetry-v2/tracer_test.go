package opentelemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func nestedSpansSingleTracerWorkflow(recorder *tracetest.SpanRecorder) func(workflow.Context) error {
	return func(ctx workflow.Context) error {
		provider := NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		tracer := NewTracer(provider, "test")

		outerCtx, outer := tracer.Start(ctx, "outer")
		middleCtx, middle := tracer.Start(outerCtx, "middle")
		_, inner := tracer.Start(middleCtx, "inner")

		inner.End()
		middle.End()
		outer.End()
		return nil
	}
}

func nestedSpansMultipleTracersWorkflow(recorder *tracetest.SpanRecorder) func(workflow.Context) error {
	return func(ctx workflow.Context) error {
		provider := NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		tracerA := NewTracer(provider, "a")
		tracerB := NewTracer(provider, "b")

		outerCtx, outer := tracerA.Start(ctx, "outer")
		innerCtx, inner := tracerB.Start(outerCtx, "inner")
		_, leaf := tracerA.Start(innerCtx, "leaf")

		leaf.End()
		inner.End()
		outer.End()
		return nil
	}
}

func spanInQueryHandlerWorkflow(recorder *tracetest.SpanRecorder) func(workflow.Context) error {
	return func(ctx workflow.Context) error {
		provider := NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		tracer := NewTracer(provider, "test")

		rootCtx, root := tracer.Start(ctx, "root")

		if err := workflow.SetQueryHandler(rootCtx, "testQuery", func() (string, error) {
			_, span := tracer.StartUnsequenced(rootCtx, "query-span")
			span.End()
			return "ok", nil
		}); err != nil {
			return err
		}

		if err := workflow.Sleep(rootCtx, time.Second); err != nil {
			return err
		}
		root.End()
		return nil
	}
}

func spanInUpdateValidatorWorkflow(recorder *tracetest.SpanRecorder) func(workflow.Context) error {
	return func(ctx workflow.Context) error {
		provider := NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		tracer := NewTracer(provider, "test")

		rootCtx, root := tracer.Start(ctx, "root")

		var updateRan bool
		if err := workflow.SetUpdateHandlerWithOptions(rootCtx, "testUpdate",
			func(ctx workflow.Context) error {
				updateRan = true
				return nil
			},
			workflow.UpdateHandlerOptions{
				Validator: func(ctx workflow.Context) error {
					_, span := tracer.StartUnsequenced(rootCtx, "validate-span")
					span.End()
					return nil
				},
			}); err != nil {
			return err
		}

		if err := workflow.Await(rootCtx, func() bool { return updateRan }); err != nil {
			return err
		}
		root.End()
		return nil
	}
}

func nestedSpansGlobalProviderWorkflow(ctx workflow.Context) error {
	tracer := NewTracer(otel.GetTracerProvider(), "test")

	outerCtx, outer := tracer.Start(ctx, "outer")
	_, inner := tracer.Start(outerCtx, "inner")

	inner.End()
	outer.End()
	return nil
}

func TestTracerProviderSpanTree(t *testing.T) {
	t.Run("single tracer", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		wf := nestedSpansSingleTracerWorkflow(recorder)
		env.RegisterWorkflow(wf)
		env.ExecuteWorkflow(wf)
		require.NoError(t, env.GetWorkflowError())

		require.Equal(t, []string{
			"outer",
			"  middle",
			"    inner",
		}, SpanTree(recorder.Ended()))
	})

	t.Run("multiple tracers", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		wf := nestedSpansMultipleTracersWorkflow(recorder)
		env.RegisterWorkflow(wf)
		env.ExecuteWorkflow(wf)
		require.NoError(t, env.GetWorkflowError())

		require.Equal(t, []string{
			"outer",
			"  inner",
			"    leaf",
		}, SpanTree(recorder.Ended()))
	})

	t.Run("query handler", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		wf := spanInQueryHandlerWorkflow(recorder)
		env.RegisterWorkflow(wf)

		env.RegisterDelayedCallback(func() {
			_, err := env.QueryWorkflow("testQuery")
			require.NoError(t, err)
		}, time.Millisecond)

		env.ExecuteWorkflow(wf)
		require.NoError(t, env.GetWorkflowError())

		require.Equal(t, []string{
			"root",
			"  query-span",
		}, SpanTree(recorder.Ended()))
	})

	t.Run("update validator", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		wf := spanInUpdateValidatorWorkflow(recorder)
		env.RegisterWorkflow(wf)

		env.RegisterDelayedCallback(func() {
			env.UpdateWorkflow("testUpdate", "updateID", &testsuite.TestUpdateCallback{})
		}, time.Millisecond)

		env.ExecuteWorkflow(wf)
		require.NoError(t, env.GetWorkflowError())

		require.Equal(t, []string{
			"root",
			"  validate-span",
		}, SpanTree(recorder.Ended()))
	})

	t.Run("global provider", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		prev := otel.GetTracerProvider()
		otel.SetTracerProvider(NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
		defer otel.SetTracerProvider(prev)

		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(nestedSpansGlobalProviderWorkflow)
		env.ExecuteWorkflow(nestedSpansGlobalProviderWorkflow)
		require.NoError(t, env.GetWorkflowError())

		require.Equal(t, []string{
			"outer",
			"  inner",
		}, SpanTree(recorder.Ended()))
	})
}
