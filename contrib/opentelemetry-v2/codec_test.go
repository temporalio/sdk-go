package opentelemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type codecTestSuite struct {
	suite.Suite
	propagator            testPropagator
	invalidSpanPropagator testPropagator
	testBaggage           baggage.Baggage
	malformedSpan         trace.Span
	validSpan             trace.Span
}

func TestCodecTestSuite(t *testing.T) {
	suite.Run(t, new(codecTestSuite))
}

func (s *codecTestSuite) SetupSuite() {
	testBaggage, err := baggage.Parse("key=value")
	s.Require().NoError(err)
	s.testBaggage = testBaggage

	validSpanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})
	malformedSpanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
	})
	s.validSpan = trace.SpanFromContext(trace.ContextWithSpanContext(context.Background(), validSpanContext))
	s.malformedSpan = trace.SpanFromContext(trace.ContextWithSpanContext(context.Background(), malformedSpanContext))

	s.propagator = testPropagator{
		spanContext: validSpanContext,
		baggage:     s.testBaggage,
	}
	s.invalidSpanPropagator = testPropagator{
		spanContext: malformedSpanContext,
		baggage:     s.testBaggage,
	}
}

func (s *codecTestSuite) codec(options PluginOptions) *spanCodec {
	opts := newOptions(options)
	return &spanCodec{contextBridge: &contextBridge{options: opts}}
}

func (s *codecTestSuite) TestMarshalSpan() {
	tests := []struct {
		name    string
		options PluginOptions
		span    *tracerSpan
		headers map[string]string
	}{
		{
			name:    "success",
			options: PluginOptions{TextMapPropagator: s.propagator},
			span:    &tracerSpan{Span: s.validSpan, Baggage: s.testBaggage},
			headers: map[string]string{"span": "ok", "baggage": "ok"},
		},
		{
			name:    "malformed span",
			options: PluginOptions{TextMapPropagator: s.invalidSpanPropagator},
			span:    &tracerSpan{Span: s.malformedSpan, Baggage: s.invalidSpanPropagator.baggage},
			headers: map[string]string{"baggage": "ok"},
		},
		{
			name:    "baggage disabled",
			options: PluginOptions{TextMapPropagator: s.propagator, DisableBaggage: true},
			span:    &tracerSpan{Span: s.validSpan, Baggage: s.testBaggage},
			headers: map[string]string{"span": "ok"},
		},
		{
			name:    "only span",
			options: PluginOptions{TextMapPropagator: s.propagator},
			span:    &tracerSpan{Span: s.validSpan},
			headers: map[string]string{"span": "ok"},
		},
		{
			name:    "only baggage",
			options: PluginOptions{TextMapPropagator: s.propagator},
			span:    &tracerSpan{Baggage: s.testBaggage},
			headers: map[string]string{"baggage": "ok"},
		},
		{
			name:    "empty",
			options: PluginOptions{TextMapPropagator: s.propagator},
			span:    &tracerSpan{},
			headers: map[string]string{},
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			headers, err := s.codec(test.options).MarshalSpan(test.span)
			s.Require().NoError(err)
			s.Require().Equal(test.headers, headers)
		})
	}
}

func (s *codecTestSuite) TestUnmarshalSpan() {
	tests := []struct {
		name            string
		options         PluginOptions
		headers         map[string]string
		spanContext     trace.SpanContext
		baggageLen      int
		baggageKeyValue string
	}{
		{
			name:            "success",
			options:         PluginOptions{TextMapPropagator: s.propagator},
			headers:         map[string]string{"span": "ok", "baggage": "ok"},
			spanContext:     s.propagator.spanContext,
			baggageLen:      1,
			baggageKeyValue: "value",
		},
		{
			name:    "malformed headers",
			options: PluginOptions{TextMapPropagator: s.propagator},
			headers: map[string]string{"s_p_a_n": "ok", "b_a_g_g_a_g_e": "ok"},
		},
		{
			name:            "malformed span",
			options:         PluginOptions{TextMapPropagator: s.invalidSpanPropagator},
			headers:         map[string]string{"span": "ok", "baggage": "ok"},
			baggageLen:      1,
			baggageKeyValue: "value",
		},
		{
			name:        "baggage disabled",
			options:     PluginOptions{TextMapPropagator: s.propagator, DisableBaggage: true},
			headers:     map[string]string{"span": "ok", "baggage": "ok"},
			spanContext: s.propagator.spanContext,
		},
		{
			name:        "only span",
			options:     PluginOptions{TextMapPropagator: s.propagator},
			headers:     map[string]string{"span": "ok"},
			spanContext: s.propagator.spanContext,
		},
		{
			name:            "only baggage",
			options:         PluginOptions{TextMapPropagator: s.propagator},
			headers:         map[string]string{"baggage": "ok"},
			baggageLen:      1,
			baggageKeyValue: "value",
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			ref, err := s.codec(test.options).UnmarshalSpan(test.headers)
			s.Require().NoError(err)
			spanRef := asTracerSpan(ref)

			s.Require().Equal(test.spanContext.IsValid(), spanRef.SpanContext().IsValid())
			s.Require().Equal(test.spanContext.TraceID(), spanRef.SpanContext().TraceID())
			s.Require().Equal(test.spanContext.SpanID(), spanRef.SpanContext().SpanID())
			s.Require().Equal(test.baggageLen, spanRef.Baggage.Len())
			s.Require().Equal(test.baggageKeyValue, spanRef.Baggage.Member("key").Value())
		})
	}
}

func (s *codecTestSuite) TestTextMapCarrierGetIsCaseInsensitive() {
	carrier := textMapCarrier{"traceparent": "value"}
	s.Require().Equal("value", carrier.Get("TraceParent"))
}

func (s *codecTestSuite) TestTextMapCarrierSetPreservesCase() {
	carrier := textMapCarrier{}
	carrier.Set("Baggage", "value")
	s.Require().Equal(textMapCarrier{"Baggage": "value"}, carrier)
}

type testPropagator struct {
	spanContext trace.SpanContext
	baggage     baggage.Baggage
}

func (p testPropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.TraceID() == p.spanContext.TraceID() && spanContext.SpanID() == p.spanContext.SpanID() {
		carrier.Set("span", "ok")
	}
	if baggage.FromContext(ctx).Len() > 0 {
		carrier.Set("baggage", "ok")
	}
}

func (p testPropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	if carrier.Get("span") == "ok" {
		ctx = trace.ContextWithRemoteSpanContext(ctx, p.spanContext)
	}
	if carrier.Get("baggage") == "ok" {
		ctx = baggage.ContextWithBaggage(ctx, p.baggage)
	}
	return ctx
}

func (testPropagator) Fields() []string { return []string{"span", "baggage"} }
