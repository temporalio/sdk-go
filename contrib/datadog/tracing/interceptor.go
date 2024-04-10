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

// Package tracing provides Datadog tracing utilities
package tracing

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

// TracerOptions are options provided to NewInterceptor
type TracerOptions struct {
	// DisableSignalTracing can be set to disable signal tracing.
	DisableSignalTracing bool

	// DisableQueryTracing can be set to disable query tracing.
	DisableQueryTracing bool

	// CheckError can be set to a custom function to determine if an error should be traced.
	CheckError func(err error) bool
}

// NewTracingInterceptor convenience method that wraps a NeTracer() with a tracing interceptor
func NewTracingInterceptor(opts TracerOptions) interceptor.Interceptor {
	return interceptor.NewTracingInterceptor(NewTracer(opts))

}

// NewTracer creates an interceptor for setting on client options
// that implements Datadog tracing for workflows.
func NewTracer(opts TracerOptions) interceptor.Tracer {
	return &tracerImpl{
		opts: TracerOptions{
			DisableSignalTracing: opts.DisableSignalTracing,
			DisableQueryTracing:  opts.DisableQueryTracing,
			CheckError:           opts.CheckError,
		},
	}
}

type contextKey string

const (
	activeSpanContextKey contextKey = "dd_trace_span"
	headerKey                       = string(activeSpanContextKey)
)

type tracerImpl struct {
	interceptor.BaseTracer
	// DisableSignalTracing can be set to disable signal tracing.
	opts TracerOptions
}

func (t *tracerImpl) Options() interceptor.TracerOptions {
	return interceptor.TracerOptions{
		SpanContextKey:       activeSpanContextKey,
		HeaderKey:            headerKey,
		DisableSignalTracing: t.opts.DisableSignalTracing,
		DisableQueryTracing:  t.opts.DisableQueryTracing,
	}
}

func (t *tracerImpl) UnmarshalSpan(m map[string]string) (interceptor.TracerSpanRef, error) {
	var carrier tracer.TextMapCarrier = m
	ctx, err := tracer.Extract(carrier)
	if err != nil {
		if errors.Is(err, tracer.ErrSpanContextNotFound) {
			// If there is no span, return nothing, but don't error out. This is
			// a legitimate place where a span does not exist in the headers
			return nil, nil
		}
		return nil, err
	}
	return &tracerSpanCtx{ctx}, nil
}

func (t *tracerImpl) MarshalSpan(span interceptor.TracerSpan) (map[string]string, error) {
	carrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(span.(*tracerSpan).Context(), carrier); err != nil {
		return nil, err
	}
	return carrier, nil
}

func (t *tracerImpl) SpanFromContext(ctx context.Context) interceptor.TracerSpan {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return nil
	}
	return &tracerSpan{CheckError: t.opts.CheckError, Span: span}
}

func (t *tracerImpl) ContextWithSpan(ctx context.Context, span interceptor.TracerSpan) context.Context {
	return tracer.ContextWithSpan(ctx, span.(*tracerSpan).Span)
}

func genSpanID(idempotencyKey string) uint64 {
	h := fnv.New64()
	// Write() always writes all bytes and never fails; the count and error result are for implementing io.Writer.
	_, _ = h.Write([]byte(idempotencyKey))
	return h.Sum64()
}

func (t *tracerImpl) StartSpan(options *interceptor.TracerStartSpanOptions) (interceptor.TracerSpan, error) {
	startOpts := []tracer.StartSpanOption{
		tracer.ResourceName(options.Name),
		tracer.StartTime(options.Time),
	}
	// Set a deterministic span ID for workflows which are long-running and cross process boundaries
	if options.IdempotencyKey != "" {
		startOpts = append(startOpts, tracer.WithSpanID(genSpanID(options.IdempotencyKey)))
	}

	// Add parent span to start options
	var parent ddtrace.SpanContext
	switch opParent := options.Parent.(type) {
	case nil:
	case *tracerSpan:
		parent = opParent.Context()
	case *tracerSpanCtx:
		parent = opParent
	default:
		// This should be considered an error, because something unexpected is
		// in the place where only a parent trace should be. In this case, we don't
		// set up the parent, so we will be creating a new top-level span
	}
	if parent != nil {
		// Convert the parent context into a full trace.
		// This is required, otherwise the implementation of StartSpan()
		// will NOT respect ChildOf().
		// The runtime type of parentTrace will be tracer.spanContext (note the
		// starting lowercase on "spanContext", that's an internal struct)
		parentTrace, err := tracer.Extract(newSpanContextReader(parent))
		if err != nil {
			return nil, err
		}
		startOpts = append(startOpts, tracer.ChildOf(parentTrace))
	}

	// Add tags to start options
	for k, v := range options.Tags {
		// TODO when custom span support is added we might have to revisit this
		// Display Temporal tags in a nested group in Datadog APM
		tagKey := "temporal." + strings.TrimPrefix(k, "temporal")
		startOpts = append(startOpts, tracer.Tag(tagKey, v))
	}

	// Start and return span
	s := tracer.StartSpan(t.SpanName(options), startOpts...)
	return &tracerSpan{CheckError: t.opts.CheckError, Span: s}, nil
}

func (t *tracerImpl) GetLogger(logger log.Logger, ref interceptor.TracerSpanRef) log.Logger {
	spanRef, ok := ref.(*tracerSpan)
	if !ok {
		logger.Error(fmt.Sprintf("Error injecting TraceID in GetLogger: TracerSpanRef is type %T, expected *tracerSpan", ref))
		return logger
	}
	return log.With(logger, "dd.trace_id", spanRef.TraceID(), "dd.span_id", spanRef.SpanID())
}

func (t *tracerImpl) SpanName(options *interceptor.TracerStartSpanOptions) string {
	return fmt.Sprintf("temporal.%s", options.Operation)
}

func newSpanContextReader(parent ddtrace.SpanContext) spanContextReader {
	carrier := map[string]string{
		tracer.DefaultTraceIDHeader:  strconv.FormatUint(parent.TraceID(), 10),
		tracer.DefaultParentIDHeader: strconv.FormatUint(parent.SpanID(), 10),
	}

	// attach baggage items
	parent.ForeachBaggageItem(func(k, v string) bool {
		carrier[tracer.DefaultBaggageHeaderPrefix+k] = v
		return true
	})

	return spanContextReader{
		keyMap: carrier,
	}
}

type spanContextReader struct {
	keyMap map[string]string
}

// ForeachKey implements tracer.TextMapReader
func (r spanContextReader) ForeachKey(handler func(key string, value string) error) error {
	for k, v := range r.keyMap {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

type tracerSpan struct {
	ddtrace.Span
	CheckError func(err error) bool
}
type tracerSpanCtx struct {
	ddtrace.SpanContext
}

func (t *tracerSpan) SpanID() uint64 {
	return t.Span.Context().SpanID()
}

func (t *tracerSpan) TraceID() uint64 {
	return t.Span.Context().TraceID()
}

func (t *tracerSpan) ForeachBaggageItem(handler func(k string, v string) bool) {
	t.Span.Context().ForeachBaggageItem(handler)
}

func (t *tracerSpan) Finish(options *interceptor.TracerFinishSpanOptions) {
	var opts []tracer.FinishOption

	err := options.Error
	isErrEligible := err != nil &&
		!workflow.IsContinueAsNewError(err) &&
		t.CheckError != nil && t.CheckError(err)

	if isErrEligible {
		opts = append(opts, tracer.WithError(err))
	}

	t.Span.Finish(opts...)
}
