// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package opentelemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/interceptor"
)

var DefaultTextMapPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

type TracerOptions struct {
	// If not set, the global provider is used to obtain one
	Tracer trace.Tracer

	// If not set, DefaultTextMapPropagator is used, *not* the global one
	TextMapPropagator propagation.TextMapPropagator

	SpanContextKey interface{}
	HeaderKey      string
	SpanStarter    func(ctx context.Context, t trace.Tracer, spanName string, opts ...trace.SpanStartOption) trace.Span
}

type spanContextKey struct{}

const defaultHeaderKey = "_tracer-data"

type tracer struct{ options *TracerOptions }

func NewTracer(options TracerOptions) (interceptor.Tracer, error) {
	if options.Tracer == nil {
		options.Tracer = otel.GetTracerProvider().Tracer("temporal-sdk-go")
	}
	if options.TextMapPropagator == nil {
		options.TextMapPropagator = DefaultTextMapPropagator
	}
	if options.SpanContextKey == nil {
		options.SpanContextKey = spanContextKey{}
	}
	if options.HeaderKey == "" {
		options.HeaderKey = defaultHeaderKey
	}
	if options.SpanStarter == nil {
		options.SpanStarter = func(
			ctx context.Context,
			t trace.Tracer,
			spanName string,
			opts ...trace.SpanStartOption,
		) trace.Span {
			_, span := t.Start(ctx, spanName, opts...)
			return span
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
	ctx := trace.SpanContextFromContext(t.options.TextMapPropagator.Extract(context.Background(), textMapCarrier(m)))
	if !ctx.IsValid() {
		return nil, nil
	}
	return &tracerSpanRef{SpanContext: ctx}, nil
}

func (t *tracer) MarshalSpan(span interceptor.TracerSpan) (map[string]string, error) {
	data := textMapCarrier{}
	t.options.TextMapPropagator.Inject(trace.ContextWithSpan(context.Background(), span.(*tracerSpan).Span), data)
	return map[string]string(data), nil
}

func (t *tracer) SpanFromContext(ctx context.Context) interceptor.TracerSpan {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return nil
	}
	return &tracerSpan{Span: span}
}

func (t *tracer) ContextWithSpan(ctx context.Context, span interceptor.TracerSpan) context.Context {
	return trace.ContextWithSpan(ctx, span.(*tracerSpan).Span)
}

func (t *tracer) StartSpan(opts *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {
	// Create context with parent
	var parent trace.SpanContext
	switch optParent := opts.Parent.(type) {
	case nil:
	case *tracerSpan:
		parent = optParent.SpanContext()
	case *tracerSpanRef:
		parent = optParent.SpanContext
	default:
		panic(fmt.Sprintf("unrecognized parent type %T", optParent))
	}
	ctx := context.Background()
	if parent.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, parent)
	}

	// Create span
	span := t.options.SpanStarter(ctx, t.options.Tracer, opts.Operation+":"+opts.Name)

	// Set tags
	if len(opts.Tags) > 0 {
		attrs := make([]attribute.KeyValue, 0, len(opts.Tags))
		for k, v := range opts.Tags {
			attrs = append(attrs, attribute.String(k, v))
		}
		span.SetAttributes(attrs...)
	}

	return &tracerSpan{Span: span}, nil
}

type tracerSpanRef struct{ trace.SpanContext }

type tracerSpan struct{ trace.Span }

func (t *tracerSpan) Finish(opts *interceptor.TracerFinishSpanOptions) {
	if opts.Error != nil {
		t.SetStatus(codes.Error, opts.Error.Error())
	}
	t.End()
}

type textMapCarrier map[string]string

func (t textMapCarrier) Get(key string) string        { return t[key] }
func (t textMapCarrier) Set(key string, value string) { t[key] = value }
func (t textMapCarrier) Keys() []string {
	ret := make([]string, 0, len(t))
	for k := range t {
		ret = append(ret, k)
	}
	return ret
}
