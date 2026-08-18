package opentelemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	interceptortracing "go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/workflow"
)

type contextBridgeTestSuite struct {
	suite.Suite
}

func TestContextBridgeTestSuite(t *testing.T) {
	suite.Run(t, new(contextBridgeTestSuite))
}

func (s *contextBridgeTestSuite) bridge(options PluginOptions) *contextBridge {
	return &contextBridge{options: newOptions(options)}
}

func (s *contextBridgeTestSuite) contextWithBaggage(key, value string) context.Context {
	bag, err := baggage.Parse(key + "=" + value)
	s.Require().NoError(err)
	return baggage.ContextWithBaggage(context.Background(), bag)
}

// validSpan builds a span the bridges accept, which requires a valid span context.
func (s *contextBridgeTestSuite) validSpan() *tracerSpan {
	s.T().Helper()
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})
	return &tracerSpan{Span: trace.SpanFromContext(trace.ContextWithSpanContext(context.Background(), spanContext))}
}

func (s *contextBridgeTestSuite) TestKeepsAmbientBaggage() {
	bridge := s.bridge(PluginOptions{})
	noopSpan := &tracerSpan{Span: trace.SpanFromContext(context.Background())}

	ctx := s.contextWithBaggage("key", "value")
	ctx = bridge.ContextWithSpan(ctx, noopSpan)

	s.Require().Equal("value", baggage.FromContext(ctx).Member("key").Value())
}

func (s *contextBridgeTestSuite) TestContextWithParentDefaultsToBaggageEnabled() {
	bag, err := baggage.Parse("key=value")
	s.Require().NoError(err)

	ctx := s.bridge(PluginOptions{}).ContextWithSpan(context.Background(), &tracerSpan{Baggage: bag})

	s.Require().Equal("value", baggage.FromContext(ctx).Member("key").Value())
}

func (s *contextBridgeTestSuite) TestReplacesAmbientBaggageWithSpanBaggage() {
	bridge := s.bridge(PluginOptions{})

	bag, err := baggage.Parse("key=current-value")
	s.Require().NoError(err)

	ctx := s.contextWithBaggage("key", "ambient-value")
	ctx = bridge.ContextWithSpan(ctx, &tracerSpan{Baggage: bag})

	s.Require().Equal("current-value", baggage.FromContext(ctx).Member("key").Value())
}

func (s *contextBridgeTestSuite) TestNoSpanOnContextReturnsNoopSpan() {
	bridge := &workflowContextBridge{options: newOptions(PluginOptions{})}
	span := bridge.SpanFromContext(internal.Background())

	s.Require().NotNil(span)
	s.Require().False(span.(*tracerSpan).SpanContext().IsValid())
}

func (s *contextBridgeTestSuite) TestUnsupportedSpanTypeReturnsNoopSpan() {
	ctx := workflow.WithValue(internal.Background(), spanContextKey{}, unsupportedTracerSpan{})
	bridge := &workflowContextBridge{options: newOptions(PluginOptions{})}
	span := bridge.SpanFromContext(ctx)

	s.Require().NotNil(span)
	s.Require().False(span.(*tracerSpan).SpanContext().IsValid())
}

func (s *contextBridgeTestSuite) TestNilSpanKeepsStoredSpan() {
	bridge := workflowContextBridge{options: newOptions(PluginOptions{})}

	bag, err := baggage.Parse("key=value")
	s.Require().NoError(err)
	span := s.validSpan()
	span.Baggage = bag

	ctx := bridge.ContextWithSpan(internal.Background(), span)
	stored := bridge.SpanFromContext(bridge.ContextWithSpan(ctx, nil)).(*tracerSpan)

	s.Require().Equal(span.SpanContext(), stored.SpanContext())
	s.Require().Equal("value", stored.Baggage.Member("key").Value())
}

func (s *contextBridgeTestSuite) TestUnknownSpanTypeKeepsStoredSpan() {
	bridge := workflowContextBridge{options: newOptions(PluginOptions{})}

	bag, err := baggage.Parse("key=value")
	s.Require().NoError(err)
	span := s.validSpan()
	span.Baggage = bag

	ctx := bridge.ContextWithSpan(internal.Background(), span)
	stored := bridge.SpanFromContext(bridge.ContextWithSpan(ctx, unsupportedTracerSpan{})).(*tracerSpan)

	s.Require().Equal(span.SpanContext(), stored.SpanContext())
	s.Require().Equal("value", stored.Baggage.Member("key").Value())
}

func (s *contextBridgeTestSuite) TestWorkflowContextWithSpanStoresKnownType() {
	bridge := workflowContextBridge{options: newOptions(PluginOptions{})}
	span := s.validSpan()

	ctx := internal.Background()
	newCtx := bridge.ContextWithSpan(ctx, span)

	s.Require().NotSame(ctx, newCtx)
	s.Require().Equal(span.SpanContext(), bridge.SpanFromContext(newCtx).(*tracerSpan).SpanContext())
}

type unsupportedTracerSpan struct{}

func (unsupportedTracerSpan) Finish(*interceptortracing.TracerFinishSpanOptions) {}
