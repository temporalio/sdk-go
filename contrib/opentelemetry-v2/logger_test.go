package opentelemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	sdkactivity "go.temporal.io/sdk/activity"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/workflow"
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
