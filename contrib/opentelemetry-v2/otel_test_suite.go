package opentelemetry

import (
	"context"
	"slices"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

type otelTestSuite struct {
	suite.Suite
}

func (s *otelTestSuite) setTestTracerProvider(
	opts ...sdktrace.TracerProviderOption,
) *sdktrace.TracerProvider {
	provider := NewTracerProvider(opts...)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	s.T().Cleanup(func() {
		otel.SetTracerProvider(prev)
		s.Require().NoError(provider.Shutdown(context.Background()))
	})
	return provider
}

func (s *otelTestSuite) newSpanRecorder() *tracetest.SpanRecorder {
	s.T().Helper()
	recorder := tracetest.NewSpanRecorder()
	s.setTestTracerProvider(sdktrace.WithSpanProcessor(recorder))
	return recorder
}

func (s *otelTestSuite) newTestWorkflowEnvironment(
	logger ...log.Logger,
) (*tracetest.SpanRecorder, *testsuite.TestWorkflowEnvironment) {
	s.T().Helper()
	recorder := s.newSpanRecorder()
	_, workerInterceptor := newTracingInterceptors(PluginOptions{})

	var testEnv testsuite.WorkflowTestSuite
	if len(logger) > 0 {
		testEnv.SetLogger(logger[0])
	}
	env := testEnv.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{Interceptors: []interceptor.WorkerInterceptor{workerInterceptor}})
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

func (s *otelTestSuite) requireSpanAttribute(
	span sdktrace.ReadOnlySpan,
	key attribute.Key,
) attribute.Value {
	s.T().Helper()
	index := slices.IndexFunc(span.Attributes(), func(attr attribute.KeyValue) bool {
		return attr.Key == key
	})
	s.Require().NotEqual(-1, index, "%s attribute not found on %s", key, span.Name())
	return span.Attributes()[index].Value
}

func (s *otelTestSuite) requireUniqueSpanIDs(spans []sdktrace.ReadOnlySpan) {
	s.T().Helper()
	spanNamesByID := make(map[trace.SpanID]string, len(spans))
	for _, span := range spans {
		spanID := span.SpanContext().SpanID()
		previousName, duplicate := spanNamesByID[spanID]
		s.Require().False(duplicate, "span %q shares an ID with span %q", span.Name(), previousName)
		spanNamesByID[spanID] = span.Name()
	}
}

func (s *otelTestSuite) devServer() *testsuite.DevServer {
	s.T().Helper()
	srv, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{})
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = srv.Stop() })
	return srv
}
