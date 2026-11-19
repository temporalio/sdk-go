package opentelemetry

import (
	"context"
	"slices"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

type otelTestSuite struct {
	suite.Suite
}

func (s *otelTestSuite) SetupSuite() {
	worker.SetStickyWorkflowCacheSize(0)
}

func (s *otelTestSuite) newSpanRecorder() *tracetest.SpanRecorder {
	s.T().Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := NewReplaySafeTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	s.T().Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		s.Require().NoError(provider.Shutdown(context.Background()))
	})
	return recorder
}

func (s *otelTestSuite) newTestWorkflowEnvironment(
	logger ...log.Logger,
) (*tracetest.SpanRecorder, *testsuite.TestWorkflowEnvironment) {
	s.T().Helper()
	recorder := s.newSpanRecorder()
	plugin, err := NewPlugin(PluginOptions{
		TracerOptions: tracing.TracerOptions{AddTemporalSpans: true},
	})
	s.Require().NoError(err)

	var testEnv testsuite.WorkflowTestSuite
	if len(logger) > 0 {
		testEnv.SetLogger(logger[0])
	}
	env := testEnv.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{Plugins: []worker.Plugin{plugin}})
	return recorder, env
}

func (s *otelTestSuite) requireSpanNamed(
	spans []sdktrace.ReadOnlySpan,
	name string,
) sdktrace.ReadOnlySpan {
	s.T().Helper()
	index := slices.IndexFunc(spans, func(span sdktrace.ReadOnlySpan) bool {
		return span.Name() == name
	})
	s.Require().NotEqual(-1, index, "%s span not found", name)
	return spans[index]
}
