package opentelemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/temporal"
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

type interceptorTestSuite struct {
	otelTestSuite
}

func TestInterceptorTestSuite(t *testing.T) {
	suite.Run(t, new(interceptorTestSuite))
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
}
