package opentelemetry

import (
	"context"
	cryptorand "crypto/rand"

	"go.opentelemetry.io/otel"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/workflow"
)

func newTracingInterceptors(
	options PluginOptions,
) (interceptor.ClientInterceptor, interceptor.WorkerInterceptor) {
	cfg := newTracerConfig(options)
	codec := spanCodec{tracerConfig: cfg}

	workflowTracerFactory := func() tracing.WorkflowTracer {
		return &workflowInterceptorTracer{
			spanCodec: codec,
		}
	}

	tracerFactory := func() tracing.Tracer {
		return &interceptorTracer{
			spanCodec:     codec,
			contextBridge: contextBridge{options: cfg.options},
		}
	}

	return tracing.NewTracingInterceptor(tracerFactory, workflowTracerFactory)
}

// workflowInterceptorTracer creates spans for workflow interceptors.
type workflowInterceptorTracer struct {
	workflowContextBridge
	spanCodec
}

func (t *workflowInterceptorTracer) CreateSpan(
	ctx workflow.Context,
	opts *tracing.TracerStartSpanOptions,
) tracing.TracerSpan {
	// Outside a read-only context, exported workflow spans have two lifecycles:
	// 1. Start live using wall-clock time, then end live using wall-clock time.
	// 2. Start during replay using server time, then end live using wall-clock time.
	if opts.Time.IsZero() && !workflow.IsReadOnly(ctx) && workflow.IsReplaying(ctx) {
		opts.Time = workflow.Now(ctx)
	}

	span := t.buildSpan(
		context.Background(),
		otel.Tracer("temporal-sdk-go"),
		parentFromRef(opts.Parent),
		t.SpanName(opts),
		interceptorReader(ctx),
		opts,
	)

	if workflow.IsReadOnly(ctx) {
		return span
	}

	return &interceptorWorkflowSpan{
		tracerSpan: span,
		ctx:        ctx,
	}
}

// interceptorWorkflowSpan suppresses Finish calls during replay.
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

// interceptorTracer creates spans for client, activity, and nexus interceptors.
type interceptorTracer struct {
	contextBridge
	spanCodec
}

func (t *interceptorTracer) CreateSpan(ctx context.Context, opts *tracing.TracerStartSpanOptions) tracing.TracerSpan {
	return t.buildSpan(
		ctx,
		otel.Tracer("temporal-sdk-go"),
		parentFromRef(opts.Parent),
		t.SpanName(opts),
		cryptorand.Reader,
		opts,
	)
}
