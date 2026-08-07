package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/workflow"
)

// WorkflowTracer starts OpenTelemetry spans from workflow code. Sequenced spans
// have replay-stable IDs and start times. Read-only spans use ordinary random
// IDs and wall-clock start times.
//
// NOTE: Experimental
type WorkflowTracer interface {
	Start(workflow.Context, string, ...trace.SpanStartOption) (workflow.Context, trace.Span)
}

type workflowTracer struct {
	tracer trace.Tracer
}

// Tracer returns a WorkflowTracer for the given instrumentation name, like
// otel.Tracer. The tracer resolves from the global provider when this is called.
// Use [NewTracerProvider] for replay-stable sequenced IDs.
//
// NOTE: Experimental
func Tracer(name string) WorkflowTracer {
	return &workflowTracer{tracer: otel.Tracer(name)}
}

func (t *workflowTracer) Start(ctx workflow.Context, name string, opts ...trace.SpanStartOption) (workflow.Context, trace.Span) {
	// Outside a read-only context, exported workflow spans have two lifecycles:
	// 1. Start live using wall-clock time, then end live using wall-clock time.
	// 2. Start during replay using server time, then end live using wall-clock time.
	if !workflow.IsReadOnly(ctx) && workflow.IsReplaying(ctx) {
		opts = append([]trace.SpanStartOption{trace.WithTimestamp(workflow.Now(ctx))}, opts...)
	}

	otelCtx := context.WithValue(context.Background(), otelRandomKey{}, workflowRandomReader(ctx))
	parent := parentFromRef(ctx.Value(spanContextKey{}))
	if parent.spanContext.IsValid() {
		otelCtx = trace.ContextWithSpanContext(otelCtx, parent.spanContext)
	}
	if parent.baggage.Len() > 0 {
		otelCtx = baggage.ContextWithBaggage(otelCtx, parent.baggage)
	}

	_, span := t.tracer.Start(otelCtx, name, opts...)

	tSpan := &tracerSpan{Span: span, Baggage: parent.baggage}

	if workflow.IsReadOnly(ctx) {
		return workflow.WithValue(ctx, spanContextKey{}, tSpan), tSpan
	}

	wrapped := &workflowSpan{tracerSpan: tSpan, ctx: ctx}
	return workflow.WithValue(ctx, spanContextKey{}, wrapped), wrapped

}

// workflowSpan suppresses End calls during replay.
type workflowSpan struct {
	*tracerSpan
	ctx workflow.Context
}

func (s *workflowSpan) End(options ...trace.SpanEndOption) {
	if workflow.IsReplaying(s.ctx) {
		return
	}
	s.tracerSpan.Span.End(options...)
}
