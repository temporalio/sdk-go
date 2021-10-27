package opentracing

import (
	"context"
	"fmt"

	"github.com/opentracing/opentracing-go"
	"go.temporal.io/sdk/interceptor"
)

type TracerOptions struct {
	Tracer         opentracing.Tracer
	SpanContextKey interface{}
	HeaderKey      string
	SpanStarter    func(t opentracing.Tracer, operationName string, opts ...opentracing.StartSpanOption) opentracing.Span
}

type spanContextKey struct{}

const defaultHeaderKey = "_tracer-data"

type tracer struct {
	options *TracerOptions
}

func NewTracer(options TracerOptions) (interceptor.Tracer, error) {
	if options.Tracer == nil {
		options.Tracer = opentracing.GlobalTracer()
	}
	if options.SpanContextKey == nil {
		options.SpanContextKey = spanContextKey{}
	}
	if options.HeaderKey == "" {
		options.HeaderKey = defaultHeaderKey
	}
	if options.SpanStarter == nil {
		options.SpanStarter = func(
			t opentracing.Tracer,
			operationName string,
			opts ...opentracing.StartSpanOption,
		) opentracing.Span {
			return t.StartSpan(operationName, opts...)
		}
	}
	return &tracer{&options}, nil
}

func NewInterceptor(options TracerOptions) (interceptor.Interceptor, error) {
	t, err := NewTracer(options)
	if err != nil {
		return nil, err
	}
	return interceptor.NewTracingInterceptor(t), nil
}

func (t *tracer) SpanContextKey() interface{} { return t.options.SpanContextKey }

func (t *tracer) HeaderKey() string { return t.options.HeaderKey }

func (t *tracer) UnmarshalSpan(m map[string]string) (interceptor.TracerSpanRef, error) {
	ctx, err := t.options.Tracer.Extract(opentracing.TextMap, opentracing.TextMapCarrier(m))
	if err != nil {
		return nil, err
	}
	return &tracerSpanRef{SpanContext: ctx}, nil
}

func (t *tracer) MarshalSpan(span interceptor.TracerSpan) (map[string]string, error) {
	data := opentracing.TextMapCarrier{}
	if err := t.options.Tracer.Inject(span.(*tracerSpan).Context(), opentracing.TextMap, data); err != nil {
		return nil, err
	}
	return map[string]string(data), nil
}

func (t *tracer) SpanFromContext(ctx context.Context) interceptor.TracerSpan {
	span := opentracing.SpanFromContext(ctx)
	if span == nil {
		return nil
	}
	return &tracerSpan{span}
}

func (t *tracer) ContextWithSpan(ctx context.Context, span interceptor.TracerSpan) context.Context {
	return opentracing.ContextWithSpan(ctx, span.(*tracerSpan).Span)
}

func (t *tracer) StartSpan(opts *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {
	// Build start options
	var startOpts []opentracing.StartSpanOption

	// Link parent
	var parent opentracing.SpanContext
	switch optParent := opts.Parent.(type) {
	case nil:
	case *tracerSpan:
		parent = optParent.Context()
	case *tracerSpanRef:
		parent = optParent.SpanContext
	default:
		panic(fmt.Sprintf("unrecognized parent type %T", optParent))
	}
	if parent != nil {
		if opts.DependedOn {
			startOpts = append(startOpts, opentracing.ChildOf(parent))
		} else {
			startOpts = append(startOpts, opentracing.FollowsFrom(parent))
		}
	}
	if len(opts.Tags) > 0 {
		tags := make(opentracing.Tags, len(opts.Tags))
		for k, v := range opts.Tags {
			tags[k] = v
		}
		startOpts = append(startOpts, tags)
	}

	// Start
	return &tracerSpan{t.options.SpanStarter(t.options.Tracer, opts.Operation+":"+opts.Name, startOpts...)}, nil
}

type tracerSpanRef struct{ opentracing.SpanContext }

type tracerSpan struct{ opentracing.Span }

func (t *tracerSpan) Finish(opts *interceptor.TracerFinishSpanOptions) {
	if opts.Error {
		// Standard tag that can be bridged to OpenTelemetry
		t.SetTag("error", "true")
	}
	t.Span.Finish()
}
