package opentelemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
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

func (s *interceptorTestSuite) TestCodecHeaders() {
	for _, enabled := range []bool{false, true} {
		s.Run(map[bool]string{false: "legacy", true: "enabled"}[enabled], func() {
			recorder := s.newSpanRecorder()
			plugin, err := NewPlugin(PluginOptions{
				TracerOptions: tracing.TracerOptions{AddTemporalSpans: true, EncodeHeaders: enabled},
			})
			s.Require().NoError(err)
			c := s.newDevServerClient(client.Options{
				Plugins: []client.Plugin{plugin},
				DataConverter: converter.NewCodecDataConverter(
					converter.GetDefaultDataConverter(),
					converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true})),
			}, testsuite.DevServerOptions{})
			queue := uuid.NewString()
			w := worker.New(c, queue, worker.Options{})
			w.RegisterWorkflow(spanKindWorkflow)
			w.RegisterActivity(nopActivity)
			s.Require().NoError(w.Start())
			defer w.Stop()
			run, err := c.ExecuteWorkflow(context.Background(),
				client.StartWorkflowOptions{TaskQueue: queue}, spanKindWorkflow)
			s.Require().NoError(err)
			s.Require().NoError(run.Get(context.Background(), nil))
			history := c.GetWorkflowHistory(context.Background(), run.GetID(), run.GetRunID(),
				false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			event, err := history.Next()
			s.Require().NoError(err)
			payload := event.GetWorkflowExecutionStartedEventAttributes().GetHeader().GetFields()["_tracer-data"]
			s.Require().NotNil(payload)
			expected := "json/plain"
			if enabled {
				expected = "binary/zlib"
			}
			s.Equal(expected, string(payload.Metadata[converter.MetadataEncoding]))
			s.requireSpanNamed(recorder.Ended(), "RunActivity:nopActivity")
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
