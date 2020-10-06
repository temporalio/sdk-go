// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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

package internal

import (
	"context"
	"errors"

	"github.com/opentracing/opentracing-go"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
)

type tracingReader struct {
	reader HeaderReader
}

// This is important requirement for t.tracer.Extract to work.
var _ opentracing.TextMapReader = (*tracingReader)(nil)

func (t tracingReader) ForeachKey(handler func(key, val string) error) error {
	return t.reader.ForEachKey(func(k string, v *commonpb.Payload) error {
		var decodedValue string
		err := converter.GetDefaultDataConverter().FromPayload(v, &decodedValue)
		// This func will be called for all headers (not only tracing specific ones).
		// All tracing headers are strings and they MUST be decoded to `string`.
		// If some header can't be decoded to `string` it means that it is not tracing but something else header.
		// It is not an error from tracer prospective, so just pass it to handler and let handler handle it.
		if err != nil && !errors.Is(err, converter.ErrUnableToDecode) {
			return err
		}
		return handler(k, decodedValue)
	})
}

type tracingWriter struct {
	writer HeaderWriter
}

// This is important requirement for t.tracer.Inject to work.
var _ opentracing.TextMapWriter = (*tracingWriter)(nil)

func (t tracingWriter) Set(key, val string) {
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(val)
	t.writer.Set(key, encodedValue)
}

// tracingContextPropagator implements the ContextPropagator interface for
// tracing context propagation.
//
// Inject -> context.Context to Header - this extracts the Span from the
//		context and places the SpanContext into the Header
// Extract -> Header to context.Context - this extracts the SpanContext from
//		the header, returns a context.Context containing the SpanContext
// InjectFromWorkflow -> Context to Header - extracts a SpanContext from the
//		workflow context and puts it in the header
// ExtractToWorkflow -> Header to Context - takes the SpanContext present in
//		the header and puts it in the Context object. Does not start a new span
//		as that is started outside when the workflow is actually executed
type tracingContextPropagator struct {
	logger log.Logger
	tracer opentracing.Tracer
}

// NewTracingContextPropagator returns new tracing context propagator object
func NewTracingContextPropagator(logger log.Logger, tracer opentracing.Tracer) ContextPropagator {
	return &tracingContextPropagator{logger, tracer}
}

func (t *tracingContextPropagator) Inject(
	ctx context.Context,
	hw HeaderWriter,
) error {
	// retrieve span from context object
	span := opentracing.SpanFromContext(ctx)
	if span == nil {
		return nil
	}
	return t.tracer.Inject(span.Context(), opentracing.TextMap, tracingWriter{hw})
}

func (t *tracingContextPropagator) Extract(
	ctx context.Context,
	hr HeaderReader,
) (context.Context, error) {
	spanContext, err := t.tracer.Extract(opentracing.TextMap, tracingReader{hr})
	if err != nil {
		// did not find a tracing span, just return the current context
		return ctx, nil
	}
	return context.WithValue(ctx, activeSpanContextKey, spanContext), nil
}

func (t *tracingContextPropagator) InjectFromWorkflow(
	ctx Context,
	hw HeaderWriter,
) error {
	// retrieve span from context object
	spanContext := spanFromContext(ctx)
	if spanContext == nil {
		return nil
	}
	return t.tracer.Inject(spanContext, opentracing.TextMap, tracingWriter{hw})
}

func (t *tracingContextPropagator) ExtractToWorkflow(
	ctx Context,
	hr HeaderReader,
) (Context, error) {
	spanContext, err := t.tracer.Extract(opentracing.TextMap, tracingReader{hr})
	if err != nil {
		// did not find a tracing span, just return the current context
		return ctx, nil
	}
	return contextWithSpan(ctx, spanContext), nil
}
