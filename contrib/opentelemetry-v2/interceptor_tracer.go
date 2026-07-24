package opentelemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/workflow"
)

func newTracingInterceptor(options TracerOptions, provider trace.TracerProvider) interceptor.Interceptor {
	cfg := newTracerConfig(options)
	tracer := provider.Tracer("temporal-sdk-go")

	codec := spanCodec{tracerConfig: cfg}

	// One WorkflowTracer (and thus one id stream) per workflow run so
	// deterministic counters reset per execution and do not leak across runs.
	workflowTracerFactory := func() tracing.WorkflowTracer {
		return &workflowInterceptorTracer{
			tracerConfig: cfg,
			spanCodec:    codec,
			tracer:       tracer,
			stream:       &idStream{prefix: sdkIDPrefix},
		}
	}

	tracerFactory := func() tracing.Tracer {
		return &interceptorTracer{
			tracerConfig:  cfg,
			contextBridge: contextBridge{tracerConfig: cfg},
			spanCodec:     codec,
			tracer:        tracer,
		}
	}

	return tracing.NewTracingInterceptor(tracerFactory, workflowTracerFactory)
}

// workflowInterceptorTracer implements tracing.WorkflowTracer for SDK-managed
// workflow spans. Sequenced spans take a slot in stream (deterministic IDs) and
// are wrapped so Finish is a no-op during replay.
type workflowInterceptorTracer struct {
	tracerConfig
	workflowContextBridge
	spanCodec
	tracer trace.Tracer
	stream *idStream
}

func (t *workflowInterceptorTracer) CreateSpan(ctx workflow.Context, opts *tracing.TracerStartSpanOptions) tracing.TracerSpan {
	parent := parentFromRef(opts.Parent)

	key := ""
	if !opts.Unsequenced {
		key = t.stream.next(ctx)
	}

	span := t.buildSpan(
		t.tracer,
		parent,
		t.SpanName(opts),
		key,
		opts,
	)

	// Unsequenced spans (queries, update validation) are not in history: random
	// IDs, no replay wrap — Finish always records the wall-clock end.
	if opts.Unsequenced {
		return span
	}

	return &interceptorWorkflowSpan{tracerSpan: span, ctx: ctx}
}

// interceptorWorkflowSpan suppresses Finish during replay so the original
// wall-clock end time from the first execution is kept.
type interceptorWorkflowSpan struct {
	*tracerSpan
	ctx workflow.Context
}

func (s *interceptorWorkflowSpan) Finish(opts *tracing.TracerFinishSpanOptions) {
	if workflow.IsReplaying(s.ctx) {
		return
	}
	s.tracerSpan.Finish(opts)
}

// interceptorTracer implements tracing.Tracer for client, activity, and Nexus
// spans. It always passes an empty id key so the provider issues random IDs
// (these paths are not replayed from workflow history).
type interceptorTracer struct {
	tracerConfig
	contextBridge
	spanCodec
	tracer trace.Tracer
}

func (t *interceptorTracer) CreateSpan(ctx context.Context, opts *tracing.TracerStartSpanOptions) tracing.TracerSpan {
	parent := parentFromRef(opts.Parent)
	return t.buildSpan(
		t.tracer,
		parent,
		t.SpanName(opts),
		"",
		opts,
	)
}
