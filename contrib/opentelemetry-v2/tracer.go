package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/workflow"
)

// otelStreamKey holds the shared application id stream on a workflow.Context so
// multiple Tracers in one workflow advance one counter and never collide.
type otelStreamKey struct{}

// otelParentKey holds the current OTel span on a workflow.Context. workflow.Context
// has no native OTel span APIs, so parenting is carried this way.
type otelParentKey struct{}

// Tracer starts OpenTelemetry spans from workflow code. Prefer NewTracerProvider
// as the backing provider so span/trace IDs stay stable across retries and
// replays. The API mirrors the OpenTelemetry SDK's Tracer (Start with a name
// and options); use StartUnsequenced only where history is not recorded.
//
// NOTE: Experimental
type Tracer interface {
	// Start creates a replay-safe span: it takes the next slot in the workflow's
	// deterministic id stream, stamps the start with workflow.Now, and returns a
	// span whose End is a no-op during replay so the original wall-clock end time
	// is preserved.
	Start(workflow.Context, string, ...trace.SpanStartOption) (workflow.Context, trace.Span)

	// StartUnsequenced creates a span without advancing the deterministic id
	// stream. Required for query handlers and update validators, which are not
	// in workflow history and may run any number of times. The provider falls
	// back to a random id; start time is wall clock (not workflow.Now). End is
	// not suppressed during replay — these spans are not replayed from history.
	StartUnsequenced(workflow.Context, string, ...trace.SpanStartOption) (workflow.Context, trace.Span)
}

type tracer struct {
	otel trace.Tracer
}

// NewTracer returns a Tracer backed by provider, analogous to
// TracerProvider.Tracer in the OpenTelemetry SDK. Pass a provider from
// NewTracerProvider for deterministic workflow span IDs.
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

		// Key is hashed by the provider's ID generator into stable span/trace IDs.
		otelCtx = context.WithValue(otelCtx, otelIdKey{}, stream.next(ctx))

		opts = append(opts, trace.WithTimestamp(workflow.Now(ctx)))
	}

	parent, _ := ctx.Value(otelParentKey{}).(trace.Span)
	otelCtx = trace.ContextWithSpan(otelCtx, parent)

	_, span := t.otel.Start(otelCtx, name, opts...)

	ctx = workflow.WithValue(ctx, otelParentKey{}, span)

	if sequenced {
		return ctx, &workflowSpan{Span: span, ctx: ctx}
	}

	return ctx, span
}

func (t *tracer) Start(ctx workflow.Context, name string, opts ...trace.SpanStartOption) (workflow.Context, trace.Span) {
	return t.start(ctx, true, name, opts...)
}

func (t *tracer) StartUnsequenced(wctx workflow.Context, name string, opts ...trace.SpanStartOption) (workflow.Context, trace.Span) {
	return t.start(wctx, false, name, opts...)
}

// workflowSpan suppresses End during replay. Start time is deterministic
// (workflow.Now) and is reproduced on every replay; End uses wall clock and
// must run only once so retries/replays do not shift it.
type workflowSpan struct {
	trace.Span
	ctx workflow.Context
}

func (s *workflowSpan) End(options ...trace.SpanEndOption) {
	if workflow.IsReplaying(s.ctx) {
		return
	}
	s.Span.End(options...)
}
