package opentelemetry

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
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
	provider := NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
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

func (s *otelTestSuite) formatSpanTree(spans []sdktrace.ReadOnlySpan) []string {
	s.T().Helper()
	childrenByParent := make(map[int][]int)
	for childIndex, child := range spans {
		parentIndex := closestParentSpanIndex(spans, child)
		childrenByParent[parentIndex] = append(childrenByParent[parentIndex], childIndex)
	}
	return formatSpanChildren(spans, childrenByParent, -1, 0)
}

func closestParentSpanIndex(spans []sdktrace.ReadOnlySpan, child sdktrace.ReadOnlySpan) int {
	parentID := child.Parent().SpanID()
	closestIndex := -1
	var closestDistance time.Duration

	// Resets can emit the same span ID more than once. The nearest start time identifies the matching parent.
	for index, candidate := range spans {
		if candidate.SpanContext().SpanID() != parentID {
			continue
		}
		distance := child.StartTime().Sub(candidate.StartTime()).Abs()
		if closestIndex == -1 || distance < closestDistance {
			closestIndex = index
			closestDistance = distance
		}
	}
	return closestIndex
}

func formatSpanChildren(
	spans []sdktrace.ReadOnlySpan,
	childrenByParent map[int][]int,
	parentIndex int,
	depth int,
) []string {
	var tree []string
	for _, childIndex := range childrenByParent[parentIndex] {
		child := spans[childIndex]
		tree = append(tree, strings.Repeat("  ", depth)+child.Name())
		tree = append(tree, formatSpanChildren(spans, childrenByParent, childIndex, depth+1)...)
	}
	return tree
}

func (s *otelTestSuite) newDevServerClient(options client.Options) client.Client {
	s.T().Helper()
	srv, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{})
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = srv.Stop() })

	options.HostPort = srv.FrontendHostPort()
	c, err := client.DialContext(context.Background(), options)
	s.Require().NoError(err)
	s.T().Cleanup(c.Close)
	return c
}
