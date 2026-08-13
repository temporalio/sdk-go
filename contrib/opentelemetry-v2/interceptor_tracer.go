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
	"go.temporal.io/sdk/workflow"
)

type interceptorTracerBase struct {
	options *options
	tracer  trace.Tracer
}

// buildSpan starts an OTel span. random supplies bytes for the ID generator.
func (b *interceptorTracerBase) buildSpan(
	ctx context.Context,
	name string,
	random io.Reader,
	opts *tracing.TracerStartSpanOptions,
) *tracerSpan {
	var spanOpts []trace.SpanStartOption

	otelCtx := context.WithValue(ctx, otelRandomKey{}, random)

	otelCtx = contextWithParent(otelCtx, opts.Parent, b.options)

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

	_, span := b.tracer.Start(otelCtx, name, spanOpts...)

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

	base := interceptorTracerBase{
		options: options,
		tracer:  newReplaySafeTracer("temporal-sdk-go"),
	}

	codec := spanCodec{options: options}

	workflowTracerFactory := func() tracing.WorkflowTracer {
		return &workflowInterceptorTracer{
			options:               options,
			spanCodec:             codec,
			interceptorTracerBase: base,
			workflowContextBridge: workflowContextBridge{},
		}
	}

	tracerFactory := func() tracing.Tracer {
		return &interceptorTracer{
			options:               options,
			spanCodec:             codec,
			interceptorTracerBase: base,
			contextBridge:         contextBridge{options: options},
		}
	}

	return tracing.NewTracingInterceptor(tracerFactory, workflowTracerFactory)
}

// workflowInterceptorTracer creates spans for workflow interceptors.
type workflowInterceptorTracer struct {
	*options
	spanCodec
	interceptorTracerBase
	workflowContextBridge
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

// interceptorTracer creates spans for client, activity, and nexus interceptors.
type interceptorTracer struct {
	*options
	spanCodec
	interceptorTracerBase
	contextBridge
}

func (t *interceptorTracer) CreateSpan(ctx context.Context, opts *tracing.TracerStartSpanOptions) tracing.TracerSpan {
	return t.buildSpan(
		ctx,
		t.SpanName(opts),
		cryptorand.Reader,
		opts,
	)
}
