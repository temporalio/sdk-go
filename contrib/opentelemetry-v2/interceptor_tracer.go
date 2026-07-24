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

	// Keep one deterministic ID stream per workflow run.
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

// workflowInterceptorTracer creates replay-safe SDK workflow spans.
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

	// Unsequenced spans use random IDs and finish during replay.
	if opts.Unsequenced {
		return span
	}

	return &interceptorWorkflowSpan{tracerSpan: span, ctx: ctx}
}

// interceptorWorkflowSpan suppresses Finish during replay.
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

// interceptorTracer creates random-ID spans outside workflow history.
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
