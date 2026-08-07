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
	return &contextBridge{options: newTracerConfig(options).options}
}

func (s *contextBridgeTestSuite) contextWithBaggage(key, value string) context.Context {
	bag, err := baggage.Parse(key + "=" + value)
	s.Require().NoError(err)
	return baggage.ContextWithBaggage(context.Background(), bag)
}

func (s *contextBridgeTestSuite) TestKeepsAmbientBaggage() {
	bridge := s.bridge(PluginOptions{})
	noopSpan := &tracerSpan{Span: trace.SpanFromContext(context.Background())}

	ctx := s.contextWithBaggage("key", "value")
	ctx = bridge.ContextWithSpan(ctx, noopSpan)

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
	span := (workflowContextBridge{}).SpanFromContext(internal.Background())

	s.Require().NotNil(span)
	s.Require().False(span.(*tracerSpan).SpanContext().IsValid())
}

func (s *contextBridgeTestSuite) TestUnsupportedSpanTypeReturnsNoopSpan() {
	ctx := workflow.WithValue(internal.Background(), spanContextKey{}, unsupportedTracerSpan{})
	span := (workflowContextBridge{}).SpanFromContext(ctx)

	s.Require().NotNil(span)
	s.Require().False(span.(*tracerSpan).SpanContext().IsValid())
}

func (s *contextBridgeTestSuite) TestSkipSettingSkipsForNilSpan() {
	bridge := workflowContextBridge{}
	ctx := internal.Background()

	s.Require().Same(ctx, bridge.ContextWithSpan(ctx, nil))
}

func (s *contextBridgeTestSuite) TestSkipSettingSkipsUnknownType() {
	bridge := workflowContextBridge{}
	ctx := internal.Background()

	s.Require().Same(ctx, bridge.ContextWithSpan(ctx, unsupportedTracerSpan{}))
}

func (s *contextBridgeTestSuite) TestWorkflowContextWithSpanStoresKnownType() {
	bridge := workflowContextBridge{}
	span := &tracerSpan{Span: trace.SpanFromContext(context.Background())}

	ctx := internal.Background()
	newCtx := bridge.ContextWithSpan(ctx, span)

	s.Require().NotSame(ctx, newCtx)
	s.Require().Same(span, bridge.SpanFromContext(newCtx))
}

type unsupportedTracerSpan struct{}

func (unsupportedTracerSpan) Finish(*interceptortracing.TracerFinishSpanOptions) {}
