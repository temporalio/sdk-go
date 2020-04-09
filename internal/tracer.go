package internal

import (
	"context"

	"github.com/opentracing/opentracing-go"
	"go.uber.org/zap"
)

type tracingReader struct {
	reader HeaderReader
}

func (t tracingReader) ForeachKey(handler func(key, val string) error) error {
	return t.reader.ForEachKey(func(k string, v []byte) error {
		return handler(k, string(v))
	})
}

type tracingWriter struct {
	writer HeaderWriter
}

func (t tracingWriter) Set(key, val string) {
	t.writer.Set(key, []byte(val))
}

// tracingContextPropagator implements the ContextPropagator interface for
// tracing context propagation.
//
// Inject -> context.Context to Header - this extracts the Span from the
//		context and places the SpanContext into the Header
// Extract -> Header to context.Context - this extracts the SpanContext from
//		the header, returns a context.Context containing the SpanContext
// InjectFromWorkflow -> Context to Header - extracts a SpanContext from the
//		workflow context and puts it in the header
// ExtractToWorkflow -> Header to Context - takes the SpanContext present in
//		the header and puts it in the Context object. Does not start a new span
//		as that is started outside when the workflow is actually executed
type tracingContextPropagator struct {
	logger *zap.Logger
	tracer opentracing.Tracer
}

// NewTracingContextPropagator returns new tracing context propagator object
func NewTracingContextPropagator(logger *zap.Logger, tracer opentracing.Tracer) ContextPropagator {
	return &tracingContextPropagator{logger, tracer}
}

func (t *tracingContextPropagator) Inject(
	ctx context.Context,
	hw HeaderWriter,
) error {
	// retrieve span from context object
	span := opentracing.SpanFromContext(ctx)
	if span == nil {
		return nil
	}
	return t.tracer.Inject(span.Context(), opentracing.TextMap, tracingWriter{hw})
}

func (t *tracingContextPropagator) Extract(
	ctx context.Context,
	hr HeaderReader,
) (context.Context, error) {
	spanContext, err := t.tracer.Extract(opentracing.TextMap, tracingReader{hr})
	if err != nil {
		// did not find a tracing span, just return the current context
		return ctx, nil
	}
	return context.WithValue(ctx, activeSpanContextKey, spanContext), nil
}

func (t *tracingContextPropagator) InjectFromWorkflow(
	ctx Context,
	hw HeaderWriter,
) error {
	// retrieve span from context object
	spanContext := spanFromContext(ctx)
	if spanContext == nil {
		return nil
	}
	return t.tracer.Inject(spanContext, opentracing.HTTPHeaders, tracingWriter{hw})
}

func (t *tracingContextPropagator) ExtractToWorkflow(
	ctx Context,
	hr HeaderReader,
) (Context, error) {
	spanContext, err := t.tracer.Extract(opentracing.TextMap, tracingReader{hr})
	if err != nil {
		// did not find a tracing span, just return the current context
		return ctx, nil
	}
	return contextWithSpan(ctx, spanContext), nil
}
