package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/workflow"
)

// otelStreamKey identifies the workflow's shared application ID stream.
type otelStreamKey struct{}

// Tracer starts replay-stable OpenTelemetry spans from workflow code.
// Use StartUnsequenced only outside workflow history.
//
// NOTE: Experimental
type Tracer interface {
	// Start creates a replay-safe span with deterministic ID and start time.
	Start(workflow.Context, string, ...trace.SpanStartOption) (workflow.Context, trace.Span)

	// StartUnsequenced creates a random-ID span outside workflow history.
	StartUnsequenced(workflow.Context, string, ...trace.SpanStartOption) (workflow.Context, trace.Span)
}

type tracer struct {
	otel trace.Tracer
}

// NewTracer returns a Tracer backed by provider. Use NewTracerProvider for
// replay-stable workflow span IDs.
//
// NOTE: Experimental
func NewTracer(provider trace.TracerProvider, name string) Tracer {
	return &tracer{otel: provider.Tracer(name)}
}

func (t *tracer) start(ctx workflow.Context, sequenced bool, name string, opts ...trace.SpanStartOption) (workflow.Context, trace.Span) {
	otelCtx := context.Background()

	if sequenced {
		stream, ok := ctx.Value(otelStreamKey{}).(*idStream)
		if !ok {
			stream = &idStream{prefix: appIDPrefix}
			ctx = workflow.WithValue(ctx, otelStreamKey{}, stream)
		}

		// The provider hashes key into stable span and trace IDs.
		otelCtx = context.WithValue(otelCtx, otelIdKey{}, stream.next(ctx))

		opts = append(opts, trace.WithTimestamp(workflow.Now(ctx)))
	}

	parent, _ := ctx.Value(spanContextKey{}).(trace.Span)
	otelCtx = trace.ContextWithSpan(otelCtx, parent)

	_, span := t.otel.Start(otelCtx, name, opts...)

	tSpan := &tracerSpan{Span: span}
	if !sequenced {
		return workflow.WithValue(ctx, spanContextKey{}, tSpan), tSpan
	}

	wrapped := &workflowSpan{tracerSpan: tSpan, ctx: ctx}
	return workflow.WithValue(ctx, spanContextKey{}, wrapped), wrapped
}

func (t *tracer) Start(ctx workflow.Context, name string, opts ...trace.SpanStartOption) (workflow.Context, trace.Span) {
	return t.start(ctx, true, name, opts...)
}

func (t *tracer) StartUnsequenced(wctx workflow.Context, name string, opts ...trace.SpanStartOption) (workflow.Context, trace.Span) {
	return t.start(wctx, false, name, opts...)
}

// workflowSpan suppresses End during replay to preserve the first end time.
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
