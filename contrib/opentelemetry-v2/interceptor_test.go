package opentelemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor/tracing"
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

func benignErrorWorkflow(workflow.Context) error {
	return temporal.NewApplicationErrorWithOptions("expected error", "BenignError",
		temporal.ApplicationErrorOptions{Category: temporal.ApplicationErrorCategoryBenign})
}

func continueAsNewWorkflow(ctx workflow.Context) error {
	return workflow.NewContinueAsNewError(ctx, continueAsNewWorkflow)
}

type interceptorTestSuite struct {
	otelTestSuite
}

func TestInterceptorTestSuite(t *testing.T) {
	suite.Run(t, new(interceptorTestSuite))
}

func (s *interceptorTestSuite) TestTracingDataConverterSpanPropagation() {
	for _, enabled := range []bool{false, true} {
		name := "default"
		if enabled {
			name = "codec"
		}
		s.Run(name, func() {
			recorder := s.newSpanRecorder()
			dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
				converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
			plugin, err := NewPlugin(PluginOptions{
				TracerOptions: tracing.TracerOptions{AddTemporalSpans: true, UseDataConverter: enabled},
			})
			s.Require().NoError(err)
			var testSuite testsuite.WorkflowTestSuite
			env := testSuite.NewTestWorkflowEnvironment()
			env.SetDataConverter(dc)
			env.SetWorkerOptions(worker.Options{Plugins: []worker.Plugin{plugin}})
			env.RegisterActivity(nopActivity)
			env.ExecuteWorkflow(spanKindWorkflow)
			s.Require().NoError(env.GetWorkflowError())
			spans := recorder.Ended()
			runWorkflow := s.requireSpanNamed(spans, "RunWorkflow:spanKindWorkflow")
			startActivity := s.requireSpanNamed(spans, "StartActivity:nopActivity")
			runActivity := s.requireSpanNamed(spans, "RunActivity:nopActivity")
			s.Require().Equal(runWorkflow.SpanContext().SpanID(), startActivity.Parent().SpanID())
			s.Require().Equal(startActivity.SpanContext().SpanID(), runActivity.Parent().SpanID())
		})
	}
}

func (s *interceptorTestSuite) TestSpanKind() {
	recorder, env := s.newTestWorkflowEnvironment()
	env.RegisterActivity(nopActivity)
	env.RegisterWorkflow(spanKindWorkflow)
	env.ExecuteWorkflow(spanKindWorkflow)
	s.Require().NoError(env.GetWorkflowError())

	kinds := make(map[string]trace.SpanKind)
	for _, span := range recorder.Ended() {
		kinds[span.Name()] = span.SpanKind()
	}
	s.Require().Equal(trace.SpanKindServer, kinds["RunWorkflow:spanKindWorkflow"])
	s.Require().Equal(trace.SpanKindClient, kinds["StartActivity:nopActivity"])
	s.Require().Equal(trace.SpanKindServer, kinds["RunActivity:nopActivity"])
}

func (s *interceptorTestSuite) TestSpanErrorStatus() {
	s.Run("benign", func() {
		recorder, env := s.newTestWorkflowEnvironment()
		env.ExecuteWorkflow(benignErrorWorkflow)
		s.Require().Error(env.GetWorkflowError())
		spans := recorder.Ended()
		s.Require().Len(spans, 1)
		s.Require().Equal(codes.Unset, spans[0].Status().Code)
	})

	s.Run("error", func() {
		recorder, env := s.newTestWorkflowEnvironment()
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return errors.New("unexpected error")
		})
		s.Require().Error(env.GetWorkflowError())
		spans := recorder.Ended()
		s.Require().Len(spans, 1)
		s.Require().Equal(codes.Error, spans[0].Status().Code)
	})

	s.Run("continue-as-new", func() {
		recorder, env := s.newTestWorkflowEnvironment()
		env.ExecuteWorkflow(continueAsNewWorkflow)
		var continueAsNewErr *workflow.ContinueAsNewError
		s.Require().ErrorAs(env.GetWorkflowError(), &continueAsNewErr)
		spans := recorder.Ended()
		s.Require().Len(spans, 2)
		var runSpan sdktrace.ReadOnlySpan
		for _, sp := range spans {
			if sp.Name() == "RunWorkflow:continueAsNewWorkflow" {
				runSpan = sp
			}
		}
		s.Require().NotNil(runSpan)
		// Continue-as-new is normal control flow: no error status, no
		// recorded exception event.
		s.Require().Equal(codes.Unset, runSpan.Status().Code)
		s.Require().Empty(runSpan.Events())
	})
}
