package opentelemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/suite"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"

	sdkactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor/tracing"
	ilog "go.temporal.io/sdk/internal/log"
	temporalnexus "go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	nexusLoggerEndpointName  = "opentelemetry-v2-logger-endpoint"
	nexusLoggerServiceName   = "opentelemetry-v2-logger-service"
	nexusLoggerOperationName = "loggingOperation"
	nexusLoggerTaskQueue     = "opentelemetry-v2-logger"
)

type loggerTestSuite struct {
	otelTestSuite
}

func TestLoggerTestSuite(t *testing.T) {
	suite.Run(t, new(loggerTestSuite))
}

func (s *loggerTestSuite) TestGetLoggerAddsValidSpan() {
	logger := ilog.NewMemoryLogger()
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})
	span := &tracerSpan{Span: trace.SpanFromContext(trace.ContextWithSpanContext(context.Background(), spanContext))}

	(&interceptorTracerBase{}).GetLogger(logger, span).Info("message")
	line := logger.Lines()[0]

	s.Require().Contains(line, "TraceID "+spanContext.TraceID().String())
	s.Require().Contains(line, "SpanID "+spanContext.SpanID().String())
}

func (s *loggerTestSuite) TestGetLoggerSkipsInvalidSpan() {
	logger := ilog.NewMemoryLogger()
	span := &tracerSpan{}

	(&interceptorTracerBase{}).GetLogger(logger, span).Info("message")
	line := logger.Lines()[0]

	s.Require().NotContains(line, "TraceID")
	s.Require().NotContains(line, "SpanID")
}

func (s *loggerTestSuite) requireLogSpan(
	logger *ilog.MemoryLogger,
	spans []sdktrace.ReadOnlySpan,
	message string,
	spanName string,
) string {
	s.T().Helper()
	for _, line := range logger.Lines() {
		if strings.Contains(line, message) {
			span := s.requireSpanNamed(spans, spanName)
			s.Require().Contains(line, span.SpanContext().TraceID().String())
			s.Require().Contains(line, span.SpanContext().SpanID().String())
			return line
		}
	}
	s.Require().FailNow("log entry not captured", "no entry with message %q", message)
	return ""
}

func loggerActivity(ctx context.Context) error {
	sdkactivity.GetLogger(ctx).Info("logging activity")
	return nil
}

func loggerWorkflow(ctx workflow.Context) error {
	workflow.GetLogger(ctx).Info("logging workflow")
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	return workflow.ExecuteActivity(actx, loggerActivity).Get(actx, nil)
}

func customSpanLoggerWorkflow(ctx workflow.Context) error {
	spanCtx, span := Tracer("app").Start(ctx, "custom")
	defer span.End()

	workflow.GetLogger(spanCtx).Info("logging in custom span")
	return nil
}

func (s *loggerTestSuite) runWorkflow(
	workflowFn any,
	activities ...any,
) ([]sdktrace.ReadOnlySpan, *ilog.MemoryLogger) {
	s.T().Helper()
	logger := ilog.NewMemoryLogger()
	recorder, env := s.newTestWorkflowEnvironment(logger)
	env.RegisterWorkflow(workflowFn)
	for _, activity := range activities {
		env.RegisterActivity(activity)
	}
	env.ExecuteWorkflow(workflowFn)
	s.Require().NoError(env.GetWorkflowError())
	return recorder.Ended(), logger
}

func (s *loggerTestSuite) TestGetLoggerTraceFields() {
	s.Run("workflow and activity", func() {
		spans, logger := s.runWorkflow(loggerWorkflow, loggerActivity)
		s.requireLogSpan(logger, spans, "logging workflow", "RunWorkflow:loggerWorkflow")
		s.requireLogSpan(logger, spans, "logging activity", "RunActivity:loggerActivity")
	})

	s.Run("custom span", func() {
		spans, logger := s.runWorkflow(customSpanLoggerWorkflow)
		line := s.requireLogSpan(logger, spans, "logging in custom span", "custom")
		sdkSpan := s.requireSpanNamed(spans, "RunWorkflow:customSpanLoggerWorkflow")
		s.Require().NotContains(line, sdkSpan.SpanContext().SpanID().String())
	})
}

var nexusLoggerOp = nexus.NewSyncOperation(
	nexusLoggerOperationName,
	func(ctx context.Context, _ nexus.NoValue, _ nexus.StartOperationOptions) (nexus.NoValue, error) {
		temporalnexus.GetLogger(ctx).Info("logging nexus handler")
		return nil, nil
	},
)

func nexusLoggerWorkflow(ctx workflow.Context) error {
	nexusClient := workflow.NewNexusClient(nexusLoggerEndpointName, nexusLoggerServiceName)
	return nexusClient.ExecuteOperation(ctx, nexusLoggerOp, nil, workflow.NexusOperationOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
	}).Get(ctx, nil)
}

func (s *loggerTestSuite) TestNexusHandlerLoggerTraceFields() {
	recorder := s.newSpanRecorder()
	logger := ilog.NewMemoryLogger()

	plugin, err := NewPlugin(PluginOptions{
		TracerOptions: tracing.TracerOptions{AddTemporalSpans: true},
	})
	s.Require().NoError(err)
	c := s.newDevServerClient(client.Options{
		Plugins: []client.Plugin{plugin},
		Logger:  logger,
	}, testsuite.DevServerOptions{})

	_, err = c.OperatorService().CreateNexusEndpoint(context.Background(), &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: nexusLoggerEndpointName,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: "default",
						TaskQueue: nexusLoggerTaskQueue,
					},
				},
			},
		},
	})
	s.Require().NoError(err)

	w := worker.New(c, nexusLoggerTaskQueue, worker.Options{})
	w.RegisterWorkflow(nexusLoggerWorkflow)
	service := nexus.NewService(nexusLoggerServiceName)
	s.Require().NoError(service.Register(nexusLoggerOp))
	w.RegisterNexusService(service)
	s.Require().NoError(w.Start())

	run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        "otel-nexus-logger",
		TaskQueue: nexusLoggerTaskQueue,
	}, nexusLoggerWorkflow)
	s.Require().NoError(err)
	s.Require().NoError(run.Get(context.Background(), nil))
	w.Stop()

	s.requireLogSpan(logger, recorder.Ended(), "logging nexus handler",
		"RunStartNexusOperationHandler:"+nexusLoggerServiceName+"/"+nexusLoggerOperationName)
}
