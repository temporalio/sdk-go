package opentelemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel/trace"

	ilog "go.temporal.io/sdk/internal/log"
)

type loggerTestSuite struct {
	suite.Suite
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
