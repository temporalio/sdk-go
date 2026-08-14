package opentelemetry

import (
	"context"
	cryptorand "crypto/rand"
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

type interceptorTracerBase struct {
	tracing.BaseTracer
	options       *options
	otelTracer    trace.Tracer
	contextBridge *contextBridge
}

func (b *interceptorTracerBase) GetLogger(logger log.Logger, ref tracing.TracerSpanRef) log.Logger {
	span := asTracerSpan(ref)
	if span == nil || span.Span == nil || !span.SpanContext().IsValid() {
		return logger
	}

	logger = log.With(logger,
		"TraceID", span.SpanContext().TraceID(),
		"SpanID", span.SpanContext().SpanID(),
	)

	return logger
}

// buildSpan starts an OTel span. random supplies bytes for the ID generator.
func (b *interceptorTracerBase) buildSpan(
	ctx context.Context,
	random io.Reader,
	opts *tracing.TracerStartSpanOptions,
) *tracerSpan {
	var spanOpts []trace.SpanStartOption

	otelCtx := context.WithValue(ctx, otelRandomKey{}, random)

	otelCtx = b.contextBridge.ContextWithSpan(otelCtx, opts.Parent)

	switch opts.Direction {
	case tracing.SpanDirectionInbound:
		spanOpts = append(spanOpts, trace.WithSpanKind(trace.SpanKindServer))
	case tracing.SpanDirectionOutbound:
		spanOpts = append(spanOpts, trace.WithSpanKind(trace.SpanKindClient))
	default:
		spanOpts = append(spanOpts, trace.WithSpanKind(trace.SpanKindUnspecified))
	}

	if len(opts.Tags) > 0 {
		attrs := make([]attribute.KeyValue, 0, len(opts.Tags))
		for k, v := range opts.Tags {
			attrs = append(attrs, attribute.String(k, v))
		}
		spanOpts = append(spanOpts, trace.WithAttributes(attrs...))
	}

	if !opts.Time.IsZero() {
		spanOpts = append(spanOpts, trace.WithTimestamp(opts.Time))
	}

	_, span := b.otelTracer.Start(otelCtx, b.SpanName(opts), spanOpts...)

	tSpan := &tracerSpan{Span: span}
	if !b.options.DisableBaggage {
		tSpan.Baggage = baggage.FromContext(otelCtx)
	}
	return tSpan
}

func newTracingInterceptors(
	pluginOptions PluginOptions,
) (interceptor.ClientInterceptor, interceptor.WorkerInterceptor) {
	options := newOptions(pluginOptions)
	otelTracer := newReplaySafeTracer("temporal-sdk-go")
	contextBridge := &contextBridge{options: options}
	workflowContextBridge := &workflowContextBridge{options: options}
	codec := &spanCodec{contextBridge: contextBridge}
	base := &interceptorTracerBase{
		options:       options,
		contextBridge: contextBridge,
		otelTracer:    otelTracer,
	}

	tracer := &interceptorTracer{
		options:               options,
		contextBridge:         contextBridge,
		spanCodec:             codec,
		interceptorTracerBase: base,
	}

	workflowTracer := &workflowInterceptorTracer{
		options:               options,
		workflowContextBridge: workflowContextBridge,
		spanCodec:             codec,
		interceptorTracerBase: base,
	}

	return tracing.NewTracingInterceptor(tracer, workflowTracer)
}

// workflowInterceptorTracer creates spans for workflow interceptors.
type workflowInterceptorTracer struct {
	*interceptorTracerBase
	*options
	*workflowContextBridge
	*spanCodec
}

var _ tracing.WorkflowTracer = (*workflowInterceptorTracer)(nil)

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

// interceptorTracer creates spans for client, activity, and nexus interceptors.
type interceptorTracer struct {
	*interceptorTracerBase
	*options
	*contextBridge
	*spanCodec
}

var _ tracing.Tracer = (*interceptorTracer)(nil)

func (t *interceptorTracer) CreateSpan(ctx context.Context, opts *tracing.TracerStartSpanOptions) tracing.TracerSpan {
	return t.buildSpan(
		ctx,
		cryptorand.Reader,
		opts,
	)
}
